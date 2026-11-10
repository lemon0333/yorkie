# 다음 단계 결과 — 테스트 정리, Tree right-origin, skeleton 압축 검토

브랜치: `exp/right-origin-gc` (base yorkie main `72341ef5`). 로컬 커밋만 있고 push하지 않았다.
앞 단계(Text skeleton GC) 결과는 `GC-SKELETON-RESULTS.md`, Tree 명세는 `TREE-RIGHT-ORIGIN-SPEC.md`, 압축 설계는 `SKELETON-COMPACTION.md`에 있다.

## 1. 커밋 목록 (이번 단계)

| 커밋 | 내용 |
|---|---|
| `e455f468` Update text tests for insertion origins | 와이어 golden 갱신(Edit 연산에 `has_origin` = "5801"). `single_op_words`는 "섞이지 않음"만 확인하도록 변경 |
| `1bd118cd` Place concurrent Tree inserts by two origins | Tree 삽입에 right origin 기록, 부모별 자식 목록에 two-origin 배치, Tree skeleton GC(부모 목록에 남기고 자식을 버림), proto TreeNode 11–13, TreeEdit 12–13, Tree 재현·fuzz 테스트 |
| `82f4a67f` Drop content from collected Tree tombstones | Tree skeleton이 text 값과 element 속성까지 버림. 스냅샷 `skeleton_len`(15), 14번은 origin_flags용 예약. undo 시 span에서 다시 채움. skeleton에는 style 적용 안 함 |
| (이 문서 커밋) | `NEXT-STEPS-RESULTS.md`, `TREE-RIGHT-ORIGIN-SPEC.md`, `SKELETON-COMPACTION.md` |

## 2. 1단계: 실패하던 테스트 2개

- `TestForwardOperationsAreByteIdenticalOnTheWire`: 글자를 넣는 text Edit은 이제 `Edit.has_origin`(11번)을 싣는다. golden의 Edit 1개와 pack 1개만 바뀌었고, 다른 연산은 바이트가 그대로다. 테스트 주석에 이유를 적었다.
- `TestIntentRepro/single_op_words`: 같은 caret에 한 번의 연산으로 넣은 두 단어는 left와 right origin이 같아서 순서는 actor tie-break로 정해진다. `Xabc가나다Y`, `X가나다abcY` 중 하나면 통과한다(섞이지 않음만 확인).
- 결과: `go test ./pkg/... ./api/...` 전부 통과.

## 3. 2a단계: Tree 재현 결과 (적용 전)

`pkg/document/zz_tree_intent_test.go`. 기본 문서는 `<doc><p>0123456789</p></doc>`이고, 각 replica가 2–5글자를 한 글자씩 입력했다. 설정마다 300회 실행했다.

| 시나리오 | 2 replica | 3 replica |
|---|---|---|
| 문단 안 글자, 앞에서부터, 같은 caret | 0 | 0 |
| 문단 안 글자, 앞에서부터, 이웃 caret | 0 | 0 |
| 문단 안 글자, **뒤에서부터**, 같은 caret | **223** | **300** |
| 문단 안 글자, 뒤에서부터, 이웃 caret | 69 | 189 |
| 문단 안 글자, 무작위 위치, 같은 caret | 96 | 224 |
| 문단 안 글자, 무작위 위치, 이웃 caret | 17 | 105 |
| 형제 `<p>` 요소, 앞에서부터 | 0 | 0 |
| 형제 `<p>` 요소, **뒤에서부터** | **223** | **300** |

수렴은 모든 경우에 됐다(발산 0). 이유는 Text #2309와 같다. Tree 형제 배치가 createdAt 기준 RGA 규칙(`FindTreeNodesWithSplitText` 4단계)이라서, 뒤에서부터 입력하면 두 사람의 글자가 번갈아 끼어든다.

## 4. 2b단계: Tree right-origin 적용 결과

| 항목 | 적용 전 | 적용 후 |
|---|---|---|
| 섞임 (위 16개 설정) | 최대 300/300 | **16개 모두 0/300** |
| text-only Tree fuzz 20,000 (insert/delete + GC + 스냅샷 재로드) | 0 | **0** (에러 0) |
| split/merge 포함 fuzz 20,000 (GC 포함) | 296 | 325 |
| split/merge 포함 fuzz 20,000 (GC 없음) | 296 | 336 |
| origin + 물리 purge(비교용, skeleton 없음) text-only | — | 640 발산 + 5 에러 |
| `go test ./pkg/... ./api/...` | 통과 | 통과 |
| 기존 Tree 테스트 | 통과 | 통과 (기대값 수정 내역은 아래) |

split/merge 포함 fuzz는 원래 Tree에서도 발산한다. upstream에 원래 있던 문제이고, GC 없이도 296/20,000이다. 처음 origin을 적용했을 때는 391이었다. 다음 최소 재현 2건을 고쳐서 336(GC 포함 시 325)까지 줄였다.

1. seed 8253: 기본 문서 `<p>XY</p><p>ZW</p>`에서 d0가 2번째 문단 맨 앞에 a를 넣고 a 뒤에서 Enter, d1이 같은 자리에 b를 넣으면 d0는 `ba`, d1은 `ab`가 된다. 원인은 오른쪽 기준점 Z가 split으로 새 문단에 옮겨져 d0만 open 규칙을 탄 것이다. 해결: 다른 부모로 옮겨진 같은 right는 "경계 없음"으로 동등하게 보고 actor로 tie-break한다(`rightBoundsIn`).
2. seed 5089: 두 사람이 같은 지점에서 Enter를 치고, 한 사람이 그 사이에 문단 `a`를 넣는 경우다. origin이 없는 split 산물에서 스캔이 멈춰서 생겼다. 해결: split 산물(`InsPrevID`가 있는 element)은 지나간다(`isSplitProduct`).

기존 Tree 테스트 중 기대값을 고친 것(모두 이유를 주석에 적음):

- `TestTreeElementTextBoundaryUnderConcurrentEdit/another_insert_at_the_same_boundary`: origin이 같은 두 삽입은 actor 순서로 놓여 `|x`가 된다(RGA 규칙에서는 `x|`). origin을 끄면 예전 기대값을 그대로 쓴다.
- `TestTreeRestore*` 3곳: 사전 조건 "purge됐다(노드 없음)"를 "수거됐다(노드 없음 또는 skeleton)"로 바꿨다. 수렴과 결과에 대한 검사는 모두 그대로 통과한다.
- `TestTreeSplitSiblingCascade` tombstoneOrder 케이스: GC 후 형제 모양 대신, skeleton 모드에서는 내용(XML) 일치만 확인한다(6절 남은 문제 2).
- crdt `TestTreeEdit/length update after GC`, `TestToTreePosRejectsAChainEndingInAPurgedNode`: physical purge 자체를 검사하는 테스트라서 그 테스트 안에서만 `TreeSkeletonGC=false`로 실행한다.

실험 스위치(테스트 전용 env): `TREE_NO_ORIGIN=1`(Tree origin 끔), `TREE_PHYS_GC=1`(physical purge). 세 조합 모두 `pkg/document`, `pkg/document/crdt` 테스트가 통과한다.

## 5. 2c단계: Tree proto 필드 번호

js-sdk main(87a3802)의 TreeNode는 1–10, Operation.TreeEdit는 1–11을 쓴다. 겹치지 않게 골랐다.

| 메시지 | 필드 | 번호 |
|---|---|---|
| `TreeNode` | `has_origin` | 11 |
| `TreeNode` | `origin_left` | 12 |
| `TreeNode` | `origin_right` | 13 |
| `TreeNode` | (reserved: origin_flags) | 14 |
| `TreeNode` | `skeleton_len` | 15 |
| `Operation.TreeEdit` | `has_origin` | 12 |
| `Operation.TreeEdit` | `origin_right` | 13 |

coordinator 제안과 다른 점: TreeEdit에 `origin_left`를 두지 않았다. Left는 `from` 위치에서 canonical form으로 다시 계산되고 모든 replica에서 같은 값이 나오기 때문이다. TreeNode 14번은 제안대로 origin_flags용으로 예약만 했다.

참고로 Text 쪽은 TextNode 7–10(origin), 11(skeleton_len), Edit 11–14다(`GC-SKELETON-RESULTS.md`).

## 6. 3단계: skeleton 압축

`SKELETON-COMPACTION.md` 참고. 요약:

- (i) 같은 actor가 앞으로 이어서 친 입력의 skeleton을 범위 노드 하나로 묶는 방법. 의미는 바뀌지 않는다.
- (ii) 참조될 수 없는 skeleton을 실제로 지우는 방법. 연산의 version vector로 "투명 여부"를 판정하는 조건을 스케치했지만 증명이 불완전하다. 열린 문제 4개:
  1. removedAt 보존
  2. 이미 S를 origin으로 지목한 형제의 해석
  3. Tree split/merge
  4. 서버의 pending VV 보고
- **추천은 (i) 방안 A**다. 조각마다 LLRB 엔트리를 두고 노드 하나가 이를 공유한다.
- **이번 범위에서는 구현하지 않았다.** RGATreeSplit의 조회 경로 6–8곳(`findFloorNodePreferToLeft`, `findPiecesOverlapping`, `findPieceCovering`, `findRestoreAnchor`, split, restore)이 "노드 = insertion 하나의 연속 조각"을 전제하고 있어서, 고치려면 독립된 PR 규모가 된다. 그래서 비용표는 다시 재지 않았고, Text 10k/50% 수치는 `GC-SKELETON-RESULTS.md`와 같다(노드 10,000, 62.5 B/live).
- Tree skeleton은 이번 단계에서 text 값, element 속성, 자손을 버리게 바꿨다. Tree 비용표는 아직 재지 않았다.

## 7. 남은 문제

1. **Tree split/merge 발산**: 원래 Tree 296/20,000 → origin 적용 후 325. 남은 +29는 split 산물 배치(createdAt 규칙, unknown split sibling 처리)와 origin 스캔의 상호작용에서 생긴다. split 산물에도 origin을 주는 설계(산물의 Left = 원래 element, Right = 원래 element의 다음 형제)를 검토해야 한다. 원래 296건의 근본 원인(upstream)도 별도 이슈로 남아 있다.
2. **skeleton은 tombstone 순서가 replica마다 다른 상태를 고치지 못함**: 원래 Tree는 physical purge가 split 경쟁으로 어긋난 tombstone 순서를 지워 줬다. skeleton으로는 그 차이가 남는다. 보이는 내용은 같지만, 이후 그 tombstone을 창에 포함하는 삽입이 갈릴 가능성이 있다.
3. **skeleton 압축 미구현**(6절).
4. **js-sdk 미반영**: Text와 Tree의 origin, skeleton, 필드 번호를 js-sdk가 똑같이 구현해야 한다(`TREE-RIGHT-ORIGIN-SPEC.md`). 그 전까지 Go 서버의 스냅샷을 JS 클라이언트가 읽으면 skeleton을 잘못 해석한다.
5. **Wafflebase Docs 적용 전 확인할 것**: Docs는 Yorkie Tree를 쓰므로 이 엔진과 js-sdk 반영이 함께 들어가야 한다. 이번에 integration 테스트(`test/integration`, 서버 필요)는 돌리지 않았다.
6. **한 번만 나타난 실패**: `TREE_PHYS_GC=1 TREE_NO_ORIGIN=1`(main과 같은 동작) 조합에서 `TestZZTreeFuzz`가 한 번 실패했고 재현되지 않았다. 이후 3번 연속 통과했고, 2만 건 측정에서도 text-only 0이었다.
