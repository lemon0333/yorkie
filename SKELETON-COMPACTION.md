# Skeleton 누적 줄이기 — 설계 검토

대상: `exp/right-origin-gc`의 Text skeleton GC(`RGATreeSplit`)와 Tree skeleton GC.
상태: **설계 문서만. 이번 범위에서는 구현하지 않음** (이유는 4절).

## 0. 문제

skeleton은 GC된 tombstone을 내용 없이 위치만 남긴 노드다. 지금은 영원히 남는다.

- 키 입력 단위로 작성하면 글자마다 insertion(ticket)이 달라서, 현재 병합 규칙(같은 insertion이 연속일 때만)은 거의 동작하지 않는다.
- 측정(10k자, 50% 삭제 후 GC): 노드 10,000개(main 5,038), 스냅샷 315KB(62.5 B/live), heap/live 1.8배.
- skeleton 1개 비용: 스냅샷 약 32B, heap 약 600B. heap은 노드 구조체, ID와 key 캐시, ticket, splay 노드, LLRB 엔트리, origin 포인터로 구성된다.

줄이는 방향은 두 가지다.

- (i) 여러 skeleton을 **하나의 표현으로 합친다**. 의미는 그대로 두고 표현만 압축한다.
- (ii) skeleton을 **실제로 지운다**. 의미가 바뀐다.

## 1. (i) 같은 actor의 연속 입력을 범위 skeleton 하나로

### 1.1 합칠 수 있는 조건

리스트에서 인접한 skeleton a, b를 합치려면 다음을 모두 만족해야 한다.

1. a, b 모두 skeleton이다.
2. `actor(a) == actor(b)`이다.
3. b의 origin.Left가 a의 끝 경계 `(a.last.ticket, a.last.offset+len)`와 같다. 즉 b는 a 바로 뒤에 쳐진 글자다.
4. `b.origin.Right == a.origin.Right`이다. 앞으로 치는 입력은 오른쪽 기준점을 공유하므로 대부분 만족한다.
5. b.ticket이 a의 마지막 ticket보다 크다.

lamport는 **연속이 아니어도 된다**. 중간에 다른 actor의 변경을 받으면 lamport가 건너뛰기 때문이다.

조건 3·4 덕분에 합쳐진 뒤에도 각 조각의 origin을 첫 조각의 origin에서 계산해 낼 수 있다. 조각 j의 origin은 `{Left: end(j-1), Right: R}`이다. 이것은 Text split continuation과 같은 모양이라서, integrate가 이 노드를 훑을 때도 "창 안의 노드에 매달린 노드는 건너뛴다"는 기존 규칙으로 처리된다.

### 1.2 표현

```
rangeSkeleton {
  actor     ActorID
  segs      []seg          // 리스트 순서
  origin    InsertOrigin   // 첫 조각의 origin
}
seg { lamport int64; delimiter uint32; startOffset int; len int }
```

- 조각 하나당 약 24B(varint로 압축하면 스냅샷에서 3–6B)이다. 노드 하나(약 600B heap)와 비교된다.
- 스냅샷: `TextNode`에 `repeated SkeletonSegment segments`를 둔다. lamport는 앞 조각과의 차이(delta)를 varint로 쓴다.

### 1.3 treeByID 변경 범위 (핵심 비용)

지금 `treeByID`는 `(ticket, offset)` 순서의 LLRB이고, ticket 순서는 lamport → actor → delimiter다. 같은 actor의 연속 입력 사이에 다른 actor의 ticket이 끼므로, 범위 skeleton 하나를 **LLRB key 하나로 덮을 수 없다**.

- **방안 A**: 조각마다 LLRB 엔트리를 하나씩 남기고, 모두 같은 범위 노드를 가리키게 한다. 노드 구조체, splay 노드, prev/next는 하나만 남는다.
  - 조회 경로(`findFloorNode`, `findFloorNodePreferToLeft`, `findPiecesOverlapping`, `findPieceCovering`)는 받은 노드가 범위 skeleton이면 그 안에서 조각을 찾는다.
  - 절감: LLRB 엔트리(약 64B/조각)는 남고 나머지(약 500B/조각)는 사라진다.
- **방안 B**: actor별로 lamport 구간을 키로 하는 interval 맵을 두고 LLRB에서 빼낸다.
  - 절감은 최대지만, ticket 순서로 훑는 모든 코드(`findPiecesOverlapping`의 floor 반복, restore 경로)를 다시 써야 한다.

**split**: 범위 안의 `(ticket_j, k)`에서 split하면 조각 목록을 둘로 나눈다. 오른쪽 조각의 origin은 다음과 같다.

- k == 0이면 `{Left: end(j-1), Right: R}`. 원래 j번 insertion의 origin 그대로다.
- k > 0이면 continuation origin이다.

두 경우 모두 합치지 않은 replica의 상태와 같다.

**insPrev/insNext**: 범위 노드 안의 조각은 서로 다른 insertion이므로 insPrev 체인이 원래 없다. 범위 노드 바깥 경계에서만 체인을 이어 붙인다. `findFloorNodePreferToLeft`의 "id.offset == node.offset이면 insPrev" 분기를 조각 단위로 바꿔야 한다.

**restore(undo)**: span의 `createdAt`이 범위 안의 조각이면 그 조각을 떼어 낸 뒤 기존 "skeleton 다시 채우기" 경로를 탄다.

### 1.4 정확성 논증

- 범위 skeleton이 지닌 정보(ID 집합, 리스트 순서, 조각별 origin)는 합치기 전 skeleton 열과 일대일로 대응한다.
- 배치 규칙은 노드 경계가 아니라 ID, 경계, 리스트 순서만 읽는다.
- 조각 나눔은 원래도 replica마다 달랐다(Text 조각 split).

따라서 **수렴성은 바뀌지 않는다**. 위험은 의미가 아니라 구현(조회 경로 누락)에 있다.

### 1.5 기대 효과 (추정)

10k/50%, 키 입력 단위, 삭제 구간 1–20자 기준:

- 삭제된 글자 약 5,000개가 평균 약 10자짜리 구간 약 500개로 묶인다. 노드 수는 10,000에서 약 5,500으로 줄어 main(5,038)에 가까워진다.
- 스냅샷: 조각 varint 약 5B × 5,000 + 범위 노드 약 30B × 500 ≈ 40KB. 지금 약 175KB의 skeleton 부분이 대략 1/4이 된다.
- heap: 방안 A로 skeleton 부분이 약 1/8이 된다.

흩어진 1글자 삭제에서는 효과가 작다(인접한 skeleton이 적음).

## 2. (ii) 참조될 수 없는 skeleton을 실제로 지우기

### 2.1 지워도 안전하려면

skeleton S를 지워도 되는 것은, 앞으로 적용될 **어떤 연산도** 다음을 하지 않을 때다.

- (a) S를 origin(left/right)으로 지목하지 않는다.
- (b) S를 TreePos/TextPos의 ID로 쓰지 않는다.
- (c) S가 들어 있는 integrate 창에서 판단하지 않는다.

그리고 이 사실을 **모든 replica가 같은 시점에** 알아야 한다.

### 2.2 걸림돌

1. **immediate 규칙은 tombstone도 right origin으로 기록한다.** caret 바로 오른쪽이 S이면, S의 제거가 인과적으로 안정된 뒤에 만든 새 삽입도 S를 Right로 지목한다. "제거가 안정됐다"는 사실만으로는 (a)를 막지 못한다.
2. 그렇다고 로컬에서 skeleton이나 오래된 tombstone을 건너뛰고 기록하면 "live right origin" 변형이 된다. GC를 안 한 replica의 창에는 S가 남아 있어서, 작성자가 알고 있던 노드가 창 안에 들어온다. 이것이 GC 없이도 2만 건 중 138건을 발산시킨 원인이다.
3. GC 시점은 replica마다 다르다. 서버가 minVV를 건네는 시점이 다르기 때문이다. 그래서 "S를 투명하게 다룬다"는 판단이 replica 사이에서 어긋난다.

### 2.3 가능한 안전 조건 (스케치)

S를 투명하게 다룰지를 **연산마다, 그 연산의 version vector로** 정한다. Yorkie의 change는 VV를 싣는다.

- S의 **투명 시점**을 `T_S = S.removedAt`(실제 제거 ticket)로 둔다. skeleton의 removedAt을 대체 티켓으로 덮지 않고 원래 값을 남겨야 한다.
- **작성자 규칙**: 로컬 연산의 VV가 `T_S`를 덮으면(작성자가 제거를 봤으면) S를 origin과 창에서 없는 것으로 친다.
- **수신자 규칙**: 연산 op를 적용할 때 `op.vv ⊇ T_S`이면 S를 창과 해석에서 건너뛰고, 아니면 포함한다.
- 그러면 같은 연산에 대해 모든 replica가 같은 창을 본다. 창은 작성자의 VV만으로 정해진다.
- **물리 삭제 조건**: `T_S`를 덮지 않는 연산이 앞으로 하나도 오지 않음이 보장될 때 지운다. 즉 모든 클라이언트의 "앞으로 보낼 연산의 하한 VV"가 `T_S`를 덮을 때다. 서버의 minVV는 "모든 replica가 받은 것"의 하한이고, "아직 보내지 않은 로컬 변경"은 덮지 않는다. 그래서 `minVV ≥ T_S`에 더해, 각 클라이언트의 pending 로컬 변경이 모두 `T_S` 이후 VV로 만들어졌다는 조건이 필요하다(2단계 안정성).

### 2.4 증명이 불확실한 부분

1. **이미 GC된 replica와의 혼합.** VV 판정을 하려면 skeleton이 원래 removedAt을 지녀야 한다. 지금 구현은 removedAt을 대체 티켓으로 덮어 스냅샷을 줄였으므로, 그 결정을 되돌려야 한다.
2. **이미 S를 origin으로 지목한 노드.** "S가 투명한 연산"이 창을 훑다가, 투명 이전에 S를 origin으로 기록한 형제를 만나면 그 형제의 origin을 해석할 수 없다. 해석 규칙을 "가장 가까운 투명하지 않은 왼쪽/오른쪽"으로 바꿔야 하고, 그게 모든 replica에서 같은 결과를 낸다는 증명이 필요하다. 이는 사실상 YATA/Fugue의 "origin이 사라진 경우" 정리를 Yorkie의 split/조각 구조에 맞춰 다시 증명하는 일이다.
3. **Tree.** 요소 split/merge가 형제 목록을 다시 짠다. 그래서 "창"이 연산마다 같다는 전제가 원래 Tree(origin 없이)에서도 깨진다. split/merge fuzz는 2만 건 중 296건이 발산한다.
4. **2단계 안정성에 필요한 서버 지원.** 클라이언트가 "내 pending 변경의 하한 VV"를 보고하는 프로토콜이 없다.

## 3. 비교

| 항목 | (i) 범위 skeleton | (ii) 물리 삭제 |
|---|---|---|
| 의미 변경 | 없음(표현만 압축) | 있음(연산별 VV 판정, origin 해석 규칙 변경) |
| 수렴성 위험 | 낮음(조회 경로 누락 같은 구현 버그만) | 높음(증명 미완, 4개 열린 문제) |
| 프로토콜 변경 | 스냅샷 필드 추가뿐 | 연산 판정 규칙, 서버 pending-VV 보고, removedAt 보존 |
| 구현 범위 | RGATreeSplit 조회 경로 6–8곳, split/merge, 인코딩. 방안 A면 treeByID 구조 유지 | Text/Tree 배치 규칙 전체 + 서버 |
| 절감 | 노드 수가 main 수준에 근접, heap 약 1/8 | skeleton이 0이 됨 |
| js-sdk 이식 | 같은 표현만 옮기면 됨 | 같은 증명을 양쪽에서 맞춰야 함 |

## 4. 추천과 이번 범위의 결정

**추천: (i) 방안 A.** 조각마다 LLRB 엔트리를 두고 노드 하나가 공유하는 방식이다.

- 의미가 바뀌지 않으니 지금까지 확인한 수렴 결과(GC 2만 fuzz 발산 0)가 그대로 유지된다.
- 절감은 대부분 노드 구조체와 splay 쪽에서 나온다.
- (ii)는 2.4의 1–4가 풀리기 전에는 구현하면 안 된다.

**이번 범위에서는 (i)도 구현하지 않았다.** 이유는 다음과 같다.

- `findFloorNodePreferToLeft`, `findPiecesOverlapping`, `findPieceCovering`, `findRestoreAnchor`, `splitNode`, `restore`가 모두 "노드 = 한 insertion의 연속 조각"을 전제한다. 이 전제를 범위 skeleton에 맞게 바꾸는 것은 독립된 PR 규모다.
- 이번 브랜치는 Tree origin 적용까지 커져서, 같은 커밋 묶음에서 조회 경로까지 고치면 회귀를 가려내기 어렵다.

**다음 단계 제안:**

1. Text에서 방안 A를 별도 브랜치로 구현한다. 조회 경로마다 "범위 skeleton 안의 조각 찾기"를 넣는다.
2. 기존 GC 2만 fuzz와 스냅샷 재로드 fuzz로 발산 0을 확인한다.
3. 10k/50% 비용표를 다시 잰다.
4. Tree는 text 노드에만 같은 방식을 적용한다. element skeleton은 자식을 이미 버리므로 이득이 작다.
