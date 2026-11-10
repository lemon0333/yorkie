# Tree right-origin + skeleton GC 명세

기준 구현: yorkie `exp/right-origin-gc`, 커밋 `1bd118cd` + `82f4a67f` + `74c62dc1` + `d8eedb0b`.
(`74c62dc1`·`d8eedb0b`에서 바뀐 규칙은 **[개정]**으로 표시했다.)
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

- 로컬: `json.Tree.Edit`에서 **contents가 비어 있지 않거나 split_level > 0이면** **[개정]**, 편집 전 `LocalTreeOrigin(fromPos)`로 Right를 구한다. 연산에는 **Right만** 싣는다. 따라서 내용 없는 split(Enter)도 `has_origin`과 `origin_right`를 싣는다.
- 수신: Left는 연산의 `from` 위치에서 `canonicalLeft(from)`으로 다시 계산한다. from의 위치 정보만으로 정해지므로 모든 replica에서 같다.
- 와이어: `TreeEdit.has_origin`(12) = true, `origin_right`(13) = Right(nil이면 생략).
  - `has_origin=false`이면 createdAt 규칙(legacy)이다.
  - 연산 contents의 TreeNode에 실려 온 origin/skeleton 필드는 무시하고 지운다(`DropEngineOnlyLinks`).
- contents의 origin 부여(Edit Phase 8):
  - 첫 번째 최상위 content: `{Left, Right}`(연산의 origin).
  - i번째 최상위 content: `{Left: boundaryAfter(content[i-1]), Right}`.
  - 각 element content의 자손: 자기 부모 안에서 체인을 이룬다. 첫 자식은 `{Left: nil, Right: nil}`, 다음 자식은 `{Left: boundaryAfter(이전 형제), Right: nil}`.
  - `boundaryAfter(n)`은 text면 `(n.createdAt, n.offset + len(n))`, element면 `n.id`.
- 순수 삽입·순수 split(`from == to`)에서 origin을 쓰면 `to` 해석을 따로 하지 않고 `toParent, toLeft = fromParent, fromLeft`로 둔다. `to`를 createdAt 규칙으로 해석하면 동시 형제 위로 삭제 범위가 열릴 수 있기 때문이다.
- 수신 측 origin의 런타임 필드(와이어에 없음) **[개정]**:
  - `parent` = `from.ParentID`. nil Left가 "어느 부모의 맨 앞"인지를 나타낸다.
  - `cut` = contents가 비어 있음. 이 연산이 삽입이 아니라 split임을 나타낸다.
  - `vv` = 연산의 version vector. 로컬 편집이면 nil이다.

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
       if sameLeft(o, my):                               // [개정] 아래 참고
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
   - **`sameLeft(o, my)` [개정]**: 둘 중 하나라도 Left가 nil이 아니면 `sameBoundary(o.Left, my.Left)`이다. 둘 다 nil("부모의 맨 앞")이면 **같은 부모의 맨 앞일 때만** 같다고 본다.
     - o의 부모 = `o.MergedFrom`, 없으면 o가 현재 들어 있는 부모.
     - 내 부모 = `my.parent`(= from.ParentID).
     - 이유: merge가 다른 부모의 자식을 옮겨 오면, 원래 부모가 다른 "맨 앞" 노드 두 개가 한 목록에 섞인다.
5. **open 규칙** (Right가 이 부모에 없을 때, `integrateTreeOpen`):
   ```
   children += unknownSplitTail(parent, my.vv)           // [개정]
   for o in children[start..]:
       if !scanning: dest = prev
       if my.Right != nil && o.id == my.Right: break      // [개정] 내 오른쪽 기준점에 닿으면 창 끝
       prev = o; seen += o
       if o.Origin == nil: split 산물이면 continue, 아니면 break
       if sameLeft(o, my):
           if rightBoundsIn(o, o.Origin.Right, my.Right): scanning = true; continue
           my.actor < o.actor 이면 dest 앞에 삽입하고 끝; 아니면 scanning = false; continue
       if originLeftNode(o.Origin.Left) ∉ seen: break
   ```
   - `rightBoundsIn`이 true가 되려면 두 조건이 모두 필요하다. 첫째, o.Right가 nil이 아니고 my.Right와 **같지 않다**. 둘째, o.Right 노드가 **o와 같은 부모의 자식**이다 **[개정]**.
   - **`unknownSplitTail(parent, vv)` [개정]**: `parent.InsNextID` 체인을 따라가며 이 연산이 **알지 못한**(`vv`가 그 element의 생성 ticket을 덮지 않는) split 산물 element들의 자식(tombstone 포함)을 순서대로 이어 붙인다. 아는 split 산물을 만나거나 체인이 끝나면 멈춘다. 로컬 편집(`vv == nil`)은 붙이지 않는다.
     - 의미: 작성자가 보지 못한 split은, 작성자가 caret 위치를 정한 뒤에 부모를 잘랐다. 그러므로 이 연산에게 "부모의 끝"은 마지막 split 산물의 끝이다.
     - 결과 dest가 split 산물 안의 노드이면 **그 노드의 부모에** 삽입한다(`findTreeNodesWithSplitText`가 `dest.Index.Parent`를 돌려준다).
     - 최소 재현(seed 902): `<p>XY</p>`에서 d0가 Y 뒤에 a를 넣고 `XY|a`로 split, d1이 Y 뒤에 c를 넣는다. 이전 결과는 `XbYc / a` vs `XbY / ac`였고, 지금은 둘 다 `XbY / ac`다.
   - **내 Right에서 멈춤 [개정]**: 붙인 tail 안에서 내 Right 노드를 만나면 거기서 창이 끝난다. text split continuation은 Left가 내 Left와 같아서, 이 규칙이 없으면 형제처럼 비교돼 Right를 지나쳐 버린다. 최소 재현(seed 1167): `<p>ZW</p>`에서 `Z|W` split과 Z 뒤 a 삽입 → `Za / W`로 수렴.
   - 같은 Right(예: 동시 split으로 둘 다 다른 부모로 옮겨진 같은 형제)를 가리키는 두 형제는 동등하게 open이므로 actor로 tie-break한다. 이 규칙이 없으면 "split을 먼저 본 replica"와 "나중에 본 replica"가 다르게 배치한다. 최소 재현 seed 8253: `<p>ZW</p>`에서 d0가 a 입력 후 a|ZW split, d1이 b 입력 → `ba` vs `ab`.
6. **merge redirect 경로** (2-1단계: 부모가 merge로 tombstone이 됐고 leftmost) **[개정]**:
   - 기존 규칙처럼 경계(merge target 안에서 `MergedFrom == 원래 부모`인 첫 자식의 바로 앞, 없으면 target 맨 앞)를 구한다.
   - origin이 있으면 **그 경계에서 시작해** 3–5의 스캔(split이면 7의 cut)을 한다. nil Left 비교는 `sameLeft`의 부모 규칙을 따른다.
   - 최소 재현(seed 6131): d0가 p1 맨 앞에 a를 넣고 p0|p1을 merge, d2가 p1 맨 앞에 b를 넣는 경우. 이전 결과는 `XYba` vs `XYab`였고, 지금은 수렴한다.
7. **split(cut) 규칙** [개정]: contents 없이 split_level > 0인 연산(`cut`)은 삽입 위치 대신 **자르는 지점**을 구한다(`splitCutLeft`).
   - Right 노드가 이 부모의 left 뒤에 있으면 → **Right 바로 앞 형제** 뒤에서 자른다(Right와 그 뒤를 새 element로 옮긴다). left와 Right 사이의 동시 삽입은 모두 왼쪽에 남는다.
   - Right가 left 바로 뒤이면 → left 뒤에서 자른다.
   - Right가 이 부모에 없으면(끝이거나 동시 split/merge로 옮겨짐) → 부모의 마지막 자식 뒤에서 자른다(아무것도 옮기지 않음).
   - 근거: left와 Right 사이의 형제는 모두 split과 동시에 들어온 것이고, 그 삽입들은 Right를 기준으로 origin 순서가 정해졌다. Right 바로 앞에서 자르면 "split을 먼저 본 replica"에서 그 삽입들이 놓이는 쪽(open 규칙에서 Right 이전 = 왼쪽 element)과 같아진다.
   - 그 뒤 기존 Phase 2(`advancePastUnknownSplitSiblings`)와 split 로직은 그대로 실행된다.
8. contents와 split_level을 함께 가진 연산은 삽입 규칙(3–5)으로 위치를 정한 뒤 그 자리에서 split한다(기존 순서).
9. origin이 없는 연산(legacy)은 createdAt 루프를 그대로 쓴다.

## 4. 명세 질문에 대한 결정

### 4.1 merge로 자식이 다른 부모로 옮겨질 때

- 옮겨진 노드의 **origin은 바꾸지 않는다**. origin은 ID로만 해석하므로 부모와 무관하다.
- 스캔 중 o의 Left가 현재 창 밖의 노드(다른 부모 포함)로 해석되면 **창 밖 = 멈춤(break)**으로 본다. 원래 부모에서 함께 옮겨진 형제라면 창 안에서 해석되므로 계속 진행한다.
- 내 Right가 다른 부모로 옮겨졌으면 3-2에 따라 open 규칙을 쓴다.
- o의 Right가 다른 부모에 있으면 `compareRight`는 1, open 규칙에서는 "경계 없음"으로 본다.
- `mergedFrom`/`mergedInto`/`merged_at`은 origin과 독립이다. 다만 nil Left의 부모 판정에는 `MergedFrom`을 쓴다(`sameLeft`). merge-redirect 삽입은 3-6에 따라 경계에서 origin 스캔을 한다 **[개정]**.

### 4.2 split_level로 element가 쪼개질 때 새로 생기는 노드

- 새 element(split 산물, `InsPrevID != nil`)의 **origin은 nil**이다. 스캔은 이 노드를 **지나간다(continue)**. 기존 split 로직이 산물을 원래 element 바로 뒤에 두기 때문이다.
- split 연산 자체는 **right origin을 기록**하고(TreeEdit 12·13), 3-7의 cut 규칙으로 자르는 지점을 정한다 **[개정]**. 쪼개기 이전에 이미 있던 동시 삽입은 Right 앞이면 왼쪽, 뒤면 오른쪽 element에 남는다.
- 이 split을 모르는 삽입은 open 규칙에서 산물의 자식까지 이어서 훑는다(3-5 `unknownSplitTail`) **[개정]**.
- split 산물 안으로 옮겨진 text/element 자식은 **자기 origin을 그대로 유지**한다.
- 요소 split/merge가 섞인 fuzz는 이제 원래 Tree보다 적게 발산한다(6절).

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

기준값 정정: 앞선 보고의 "원래 Tree 296"은 측정 오류였다. zsh에서 `env $cfg`가 단어로 나뉘지 않아서, "origin 끔 + 물리 GC"가 실제로는 "origin 끔 + skeleton GC"로 실행됐다. 아래 값은 올바르게 다시 쟀다(`env ${=cfg}`).

| 항목 (20,000회) | 원래 Tree (origin 끔, 물리 GC) | 현재 (origin + skeleton GC) |
|---|---|---|
| 섞임 (16개 설정 × 300) | 최대 300/300 | 모두 0/300 |
| text-only fuzz (insert/delete + GC + 스냅샷 재로드) | 0 | 0 |
| split/merge 포함 fuzz, GC 포함 | **312 발산 + 6 에러** | **281 발산, 에러 0** |
| split/merge 포함 fuzz, GC 없음 | **299** | **292** |
| `go test ./pkg/... ./api/...` | 통과 | 통과 |

참고(비교용): origin + 물리 GC는 text-only에서 2,000회당 12회 발산한다. skeleton GC가 꼭 필요하다는 근거다.

남은 문제:

1. split/merge 포함 발산 292(GC 없음)는 원래 Tree의 299보다 적다. 다만 seed 단위로 보면 origin 때문에 새로 생긴 발산 65개와 origin으로 해결된 발산 72개가 섞여 있다. 남은 최소 재현(seed 1941, 13437)은 모두 **merge와 동시 split이 겹친 뒤의 삽입**이다. 원래 Tree에서 split/merge 경쟁 뒤 replica마다 tombstone 순서가 달라지고(`TestTreeSplitSiblingCascade`의 tombstoneOrder 케이스), 그 다른 창을 origin 스캔이 그대로 읽기 때문이라고 추정한다(확인은 하지 않음).
2. skeleton은 tombstone 순서가 replica마다 다른 상태를 고치지 못한다(물리 purge는 고쳐 줬음).
3. Tree skeleton 압축은 미구현이다(`SKELETON-COMPACTION.md`).
4. conformance corpus `split-product-walk-past-concurrent-insert`: 세 replica 모두 `x n`으로 수렴하지만, 기대값이 RGA 순서인 `n x`라서 실패로 기록된다. origin 규칙에서는 같은 위치·같은 origin의 두 삽입이 actor 순서(낮은 쪽이 먼저)로 놓인다. js-sdk와 함께 기대 XML을 `<doc><p>a</p><p>b</p><p>x</p><p>n</p></doc>`로 바꿔야 한다.
