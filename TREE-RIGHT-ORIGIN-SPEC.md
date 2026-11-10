# Tree right-origin + skeleton GC 명세

기준 구현: yorkie `exp/right-origin-gc`, 커밋 `1bd118cd` + `82f4a67f`.
Go 쪽 코드: `pkg/document/crdt/tree_origin.go`, `pkg/document/crdt/tree.go`.
js-sdk는 이 문서의 규칙을 그대로 옮긴다. "Text 명세"는 `GC-SKELETON-RESULTS.md`와 skeleton 명세 메시지를 가리킨다.

## 1. 개념

Tree 삽입은 부모 하나의 자식 목록 안에서 일어난다. Text의 two-origin 규칙(YjsMod/FugueMax)을 **부모별 자식 목록**에 그대로 적용한다. 자식 목록에는 tombstone과 skeleton도 포함된다.

### 1.1 경계(boundary) 표기 — canonical form

| 위치 | 표기 |
|---|---|
| 부모의 맨 앞 | `nil` |
| element E 바로 뒤 | `E.id` (offset은 E의 id 그대로) |
| text insertion C의 k-1번째 글자 바로 뒤 | `(C.createdAt, k)` (k ≥ 1, UTF-16 단위) |

주의: `TreePos.LeftSiblingID`의 element 쪽 offset은 canonical하지 않다. `toTreePos`가 자식 index를 더한다. 그래서 element 왼쪽 경계는 반드시 **element 자신의 id**로 바꿔서 쓴다(`canonicalLeft`).

### 1.2 origin

`TreeInsertOrigin { Left: boundary | nil, Right: TreeNodeID | nil }`

- **Left**: 삽입 지점의 canonical 왼쪽 경계.
- **Right**: 삽입 시점에 caret **바로 오른쪽 형제**의 ID다. tombstone과 skeleton도 포함한다(immediate 규칙).
  - caret이 text 조각 내부(조각 시작과 끝 사이)이면 `(C, k)`, 즉 같은 insertion의 다음 글자다.
  - 부모의 끝이면 nil이다.
- origin이 없는 노드(`Origin == nil`)는 createdAt 규칙으로 배치된 노드다. legacy 노드, element split 산물, 그리고 `ReissueIDs`로 ID를 새로 받은 노드가 여기에 해당한다.

## 2. 연산 (TreeEdit)

- 로컬: `json.Tree.Edit`에서 contents가 비어 있지 않으면, 편집 전 `LocalTreeOrigin(fromPos)`로 Right를 구한다. 연산에는 **Right만** 싣는다.
- 수신: Left는 연산의 `from` 위치에서 `canonicalLeft(from)`으로 다시 계산한다. from의 위치 정보만으로 정해지므로 모든 replica에서 같다.
- 와이어: `TreeEdit.has_origin`(12) = true, `origin_right`(13) = Right(nil이면 생략).
  - `has_origin=false`이면 createdAt 규칙(legacy)이다.
  - 연산 contents의 TreeNode에 실려 온 origin/skeleton 필드는 무시하고 지운다(`DropEngineOnlyLinks`).
- contents의 origin 부여(Edit Phase 8):
  - 첫 번째 최상위 content: `{Left, Right}`(연산의 origin).
  - i번째 최상위 content: `{Left: boundaryAfter(content[i-1]), Right}`.
  - 각 element content의 자손: 자기 부모 안에서 체인을 이룬다. 첫 자식은 `{Left: nil, Right: nil}`, 다음 자식은 `{Left: boundaryAfter(이전 형제), Right: nil}`.
  - `boundaryAfter(n)`은 text면 `(n.createdAt, n.offset + len(n))`, element면 `n.id`.
- 순수 삽입(`from == to`)에서 origin을 쓰면 `to` 해석을 따로 하지 않고 `toParent, toLeft = fromParent, fromLeft`로 둔다. `to`를 createdAt 규칙으로 해석하면 동시 형제 위로 삭제 범위가 열릴 수 있기 때문이다.

## 3. 배치 규칙 (수신 측 `integrateTree`)

`FindTreeNodesWithSplitText`의 4단계(형제 사이 위치 찾기)에서, origin이 있으면 createdAt 루프 대신 다음 규칙을 쓴다.

1. `realParent`의 자식 목록 `children`(tombstone과 skeleton 포함)에서 left 다음 index를 `start`로 잡는다. leftmost이면 0이다.
2. Right를 찾는다(`originRightIn`).
   - Right가 text 조각 **내부**를 가리키면 그 조각을 split해서 Right에서 시작하는 조각을 만든다.
   - `children[start:]` 안에 Right 노드가 있으면 `rightIdx`로 둔다.
   - 없으면 **"이 부모 안에 오른쪽 경계가 없음"**으로 보고 open 규칙(4)을 쓴다. 부모의 끝이거나, 동시 split/merge로 다른 부모에 옮겨진 경우다.
3. `rightIdx == start`이면(사이에 아무것도 없으면) left 바로 뒤에 넣는다.
4. **창 스캔** (left, right):
   ```
   dest, prev = left; scanning = false
   for o in children[start .. rightIdx-1]:
       if !scanning: dest = prev
       prev = o
       if o.Origin == nil:
           if o는 element이고 InsPrevID != nil:  continue   // split 산물: 지나감
           break                                            // legacy: 멈춤
       if sameBoundary(o.Origin.Left, my.Left):
           cmp = compareRight(o.Origin.Right)
           cmp < 0  → scanning = true
           cmp == 0 → my.actor < o.actor 이면 dest 앞에 삽입하고 끝; 아니면 scanning = false
           cmp > 0  → scanning = false
           continue
       oleft = originLeftNode(o.Origin.Left)
       if oleft가 창(left 포함) 밖이거나 nil: break
   (루프 끝) if !scanning: dest = prev
   ```
   - `compareRight(id)`:
     - id == Right이면 0.
     - id가 창 안의 노드(Right 노드보다 앞)이면 -1.
     - 그 밖(nil, 창 밖, Right 노드의 시작이 아닌 내부, left)이면 1.
   - `originLeftNode(b)`: nil이면 nil. element id이면 그 element. text 경계 `(C,k)`이면 글자 `(C,k-1)`을 담은 조각.
5. **open 규칙** (Right가 이 부모에 없을 때, `integrateTreeOpen`):
   ```
   for o in children[start..]:
       if !scanning: dest = prev
       prev = o; seen += o
       if o.Origin == nil: split 산물이면 continue, 아니면 break
       if sameBoundary(o.Origin.Left, my.Left):
           if rightBoundsIn(parent, o.Origin.Right, my.Right): scanning = true; continue
           my.actor < o.actor 이면 dest 앞에 삽입하고 끝; 아니면 scanning = false; continue
       if originLeftNode(o.Origin.Left) ∉ seen: break
   ```
   - `rightBoundsIn`이 true가 되려면 두 조건이 모두 필요하다. 첫째, o.Right가 nil이 아니고 my.Right와 **같지 않다**. 둘째, o.Right 노드가 **이 부모의 자식**이다.
   - 같은 Right(예: 동시 split으로 둘 다 다른 부모로 옮겨진 같은 형제)를 가리키는 두 형제는 동등하게 open이므로 actor로 tie-break한다. 이 규칙이 없으면 "split을 먼저 본 replica"와 "나중에 본 replica"가 다르게 배치한다. 최소 재현 seed 8253: `<p>ZW</p>`에서 d0가 a 입력 후 a|ZW split, d1이 b 입력 → `ba` vs `ab`.
6. merge redirect 경로(2-1단계: 부모가 merge로 tombstone이 됐고 leftmost)는 **기존 규칙을 그대로 쓴다**(origin 미사용). origin은 기록만 한다.
7. origin이 없는 연산(legacy)은 createdAt 루프를 그대로 쓴다.

## 4. 명세 질문에 대한 결정

### 4.1 merge로 자식이 다른 부모로 옮겨질 때

- 옮겨진 노드의 **origin은 바꾸지 않는다**. origin은 ID로만 해석하므로 부모와 무관하다.
- 스캔 중 o의 Left가 현재 창 밖의 노드(다른 부모 포함)로 해석되면 **창 밖 = 멈춤(break)**으로 본다. 원래 부모에서 함께 옮겨진 형제라면 창 안에서 해석되므로 계속 진행한다.
- 내 Right가 다른 부모로 옮겨졌으면 3-2에 따라 open 규칙을 쓴다.
- o의 Right가 다른 부모에 있으면 `compareRight`는 1, open 규칙에서는 "경계 없음"으로 본다.
- `mergedFrom`/`mergedInto`/`merged_at`은 origin과 독립이다. merge-redirect 삽입(3-6)은 기존 규칙으로 배치한다.

### 4.2 split_level로 element가 쪼개질 때 새로 생기는 노드

- 새 element(split 산물, `InsPrevID != nil`)의 **origin은 nil**이다. 스캔은 이 노드를 **지나간다(continue)**. 기존 split 로직이 산물을 원래 element 바로 뒤에 두기 때문이다.
- split 산물 안으로 옮겨진 text/element 자식은 **자기 origin을 그대로 유지**한다.
- 주의: 요소 split/merge가 섞인 fuzz에서 발산이 원래 Tree보다 많다(296 → 325 / 20,000). 남은 차이는 split 산물 배치 규칙(`orderSameBoundarySplit`, `advancePastUnknownSplitSiblings`)과 origin 스캔이 상호작용해서 생긴다(6절).

### 4.3 Tree text 노드 split continuation

**Text와 같은 규칙이다.** 오른쪽 조각의 origin은 `{Left: 오른쪽 조각 자신의 id (=(C, 시작 offset), 즉 "왼쪽 조각 끝 경계"), Right: 원래 Right}`이다. Left는 `originLeftNode`로 왼쪽 조각에 해석되고, 그 조각이 창 안에 있으므로 스캔은 이 조각을 지나간다. skeleton text 조각을 split할 때도 같다.

### 4.4 Tree GC skeleton의 범위와 스냅샷 표현

- **범위: text 노드와 element 노드 모두.** purge는 노드를 부모의 자식 목록에서 떼지 않고 skeleton으로 만든다.
  - **text**: Value를 버린다. UTF-16 길이를 `skelLen`에 남기고, Index의 VisibleLength/TotalLength는 그대로 둔다. ID 범위 `[offset, offset+skelLen)`는 유지된다.
  - **element**: Attrs를 버리고, tombstone인 자손을 모두 떼어 낸다. 자손은 ID 맵에서 빼고 split 체인에서도 끊는다. live 자손은 원칙적으로 없지만 있으면 남긴다.
  - skeleton 자신의 InsPrevID/InsNextID 체인과 origin, removedAt은 **유지**한다. Text와 달리 removedAt을 대체 티켓으로 바꾸지 않는다. skeleton 부모 아래에서 자식을 되살릴 때 부모의 removedAt을 쓰기 때문이다.
- **규칙**:
  - `remove`/`canDelete`/`canStyle`은 skeleton에 대해 false를 반환한다.
  - `GCPairs`(스냅샷 로드)는 skeleton을 건너뛴다. Purge(skeleton)는 no-op이다.
  - split: skeleton 조각은 GC pair나 크기 계산 없이 나뉘고, 오른쪽 조각도 skeleton이다.
  - GC 크기 계산: purge **전에** DataSize를 읽는다(`keptAsSkeleton`).
- **undo(Restore)**: skeleton은 "purge된 노드를 다시 만드는 규칙"으로 되살린다.
  - text는 span.Value에서 해당 구간을 다시 채우고, element는 span.Attributes를 복사한다(속성 tombstone은 GC pair로 등록).
  - 부모가 removed이면 **부모의 removedAt으로 tombstone 상태로** 되살리고 GC pair(GCOnlySize)를 등록한다.
  - 부모가 removed가 아니면 live로 되살리고 recreated로 보고한다.
- **스냅샷 (TreeNode)**:
  - `skeleton_len`(15)을 쓴다. text는 UTF-16 길이, element는 1이다. value는 "", attributes는 비우고, removed_at은 **필수**다.
  - origin: `has_origin`(11), `origin_left`(12, nil이면 생략=부모 맨 앞), `origin_right`(13, nil이면 생략=끝).
  - 14번은 `origin_flags`용으로 예약했다(아직 쓰지 않음).
  - 디코딩: `skeleton_len > 0`인데 removed_at이 없거나 value/attributes가 있으면 에러다. text면 Index 길이를 skeleton_len으로 맞춘다.

## 5. proto 필드 번호 (최종)

js-sdk main(87a3802)의 TreeNode는 1–10, TreeEdit는 1–11을 쓴다.

| 메시지 | 필드 | 번호 | 비고 |
|---|---|---|---|
| `TreeNode` | `has_origin` | 11 | bool |
| `TreeNode` | `origin_left` | 12 | TreeNodeID, 생략 = 부모 맨 앞 |
| `TreeNode` | `origin_right` | 13 | TreeNodeID, 생략 = 부모 끝 |
| `TreeNode` | (reserved, origin_flags) | 14 | 예약. coordinator 제안과 같은 번호, 이웃 기반 압축은 아직 미구현 |
| `TreeNode` | `skeleton_len` | 15 | uint32. text: UTF-16 길이, element: 1 |
| `Operation.TreeEdit` | `has_origin` | 12 | bool |
| `Operation.TreeEdit` | `origin_right` | 13 | TreeNodeID, 생략 = 부모 끝 |

TreeEdit에 `origin_left`가 없는 것은 coordinator 제안과 다른 점이다. 이유: Left는 `from` 위치에서 canonical form으로 다시 계산되고 모든 replica에서 같은 값이 나오므로 와이어에 실을 필요가 없다. Text의 Edit도 left가 from과 같으면 생략한다.

## 6. 검증 결과와 남은 차이

| 항목 | 결과 |
|---|---|
| 섞임 (문단 안 글자 forward/backward/random × 같은/이웃 caret × 2·3 replica, 형제 `<p>` forward/backward) | 16개 설정 모두 0/300 (이전 backward: 223–300/300) |
| text-only Tree fuzz 20,000 (insert/delete, GC, 스냅샷 재로드) | 발산 0, 에러 0 |
| split/merge 포함 fuzz 20,000 | 원래 Tree 296 → origin 적용 후 325 (GC 없음 336) |
| origin + 물리 purge(skeleton 없음) | text-only 640 발산 → skeleton 필수 |
| `go test ./pkg/... ./api/...` | 통과 |

남은 차이(+29/20,000)와 해야 할 일:

1. 요소 split 산물의 배치(동시 같은 경계 split, unknown split sibling 건너뛰기)는 createdAt 규칙을 쓰고, 일반 삽입은 origin 규칙을 쓴다. 그래서 둘이 섞인 순서가 연산 도착 순서에 따라 달라지는 경우가 남는다. 원래 Tree의 split/merge 발산(296)도 아직 해결되지 않았다.
2. skeleton은 tombstone 순서가 replica마다 다른 상태를 고치지 못한다. 기존 Tree는 physical purge가 tombstone 순서 차이를 지워 줬다(`TestTreeSplitSiblingCascade`의 tombstoneOrder 케이스). 지금 그 테스트는 skeleton 모드에서 내용(XML)만 비교한다.
3. Tree skeleton 압축(같은 actor 연속 입력 병합)은 미구현이다(`SKELETON-COMPACTION.md`).
