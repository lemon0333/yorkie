# Right-origin + Skeleton GC 결과

브랜치: `exp/right-origin-gc` (base: yorkie main `72341ef5`). 로컬 커밋만 있고 push하지 않았다.

## 1. 커밋 목록

| 커밋 | 내용 |
|---|---|
| `f6f46e64` WIP: right-origin prototype | 기존 프로토타입(커밋되지 않았던 변경)을 그대로 보존 |
| `83d5a598` Speed up right-origin placement | 성능 스레드 패치(`perf-fixes-on-prototype.patch`)를 그대로 적용. 적용 전후 실패 테스트 목록이 같음(새 실패 없음) |
| `b5bf73ef` Align text origin proto fields with yorkie-js-sdk | 스냅샷 `TextNode` origin 필드를 6–9 → 7–10으로 옮기고 `.pb.go`를 재생성 |
| `1d4da123` Keep collected text tombstones as skeletons | skeleton GC, right origin을 immediate로 고정, GC pin 제거, 테스트 추가 |
| (이 문서) | 결과 정리 |

## 2. 설계 요약

### right origin = 바로 다음 노드(immediate)
- `crdt.RightImmediate` 기본값을 `true`로 바꿨다. live 방식("다음 살아있는 글자")은 테스트에서 `RO_LIVE=1`을 줄 때만 쓰인다(`zz_variant_init_test.go`).
- live 방식은 GC 없이도 2만 시나리오 중 138건이 발산하고, immediate 방식은 0건이다(아래 표에서 다시 측정).

### skeleton GC (Yjs의 GC struct 방식)
`RGATreeSplit.Purge`는 이제 노드를 unlink하지 않고 skeleton으로 바꾼다.

- **남기는 것**: ID 범위 `[id.offset, id.offset+skelLen)`, `origin`, 리스트 위치(prev/next, insPrev/insNext, treeByID, treeByIndex)
- **버리는 것**: 내용 문자열, 속성(RHT), removedAt 티켓. 모든 skeleton은 내용이 빈 값 하나를 공유하고, removedAt 자리에는 같은 대체 티켓(`time.InitialTicket`)을 둔다.
- **보이지 않게 하기**: removedAt이 nil이 아니므로 기존 tombstone처럼 문자열·인덱스·길이 계산에서 빠진다(`Len()==0`, splay 가중치 0).
- **origin 해석**: 노드와 ID가 그대로 있으므로 `findFloorNodePreferToLeft`와 `originRightNode`, 그리고 `integrate`가 훑는 구간이 GC한 replica와 하지 않은 replica에서 같다. 원래 문제였던 비대칭이 이것으로 사라진다.
- **동작 규칙**:
  - `Remove`: skeleton에는 아무것도 하지 않는다(main에서 노드가 없어 아무 일도 없는 것과 같다).
  - `canStyle`: false를 반환한다(main과 같다).
  - GC 대상 목록(`Text.GCPairs`)에서 제외한다.
  - `splitNode`: 크기 계산이나 GC pair 없이 skeleton을 두 개로 나눈다.
  - undo의 `restore`: skeleton 구간을 span 값으로 **원래 자리에서** 다시 채운다. main은 purge된 구간을 근사 위치에 다시 만들었다.
- **병합**: 같은 insertion에서 나와 offset이 이어지고 리스트에서도 인접한 skeleton끼리 합친다. 이 경우 `b.insPrev == a`가 보장된다. 조각이 나뉘는 방식은 원래도 replica마다 달랐으므로, 병합해도 배치 결과는 달라지지 않는다.
- **GC 크기 계산**: Text 노드는 purge 후 내용이 비므로, `Root.collect`에서 skeleton이 되는 child만 purge **전에** DataSize를 읽는다. Tree/Array 경로는 동작이 같다.
- **GC pin 제거**: 프로토타입이 쓰던 `GCPin`/`IsPinned`/`originRefs`는 skeleton으로 필요 없어져서 지웠다(`gc.go`와 `root.go`는 main과 같아졌다).
- **스냅샷**: `TextNode.skeleton_len`(11번)을 추가했다. skeleton은 value가 비어 있고 attributes와 removed_at 없이 인코딩된다. 디코딩하면 GC한 replica와 같은 상태가 되고, GC pair도 생기지 않는다.

변경 파일(커밋 `1d4da123`): `pkg/document/crdt/{rga_tree_split.go,text.go,root.go,gc.go,root_test.go}`, `api/converter/{to_bytes.go,from_bytes.go}`, `api/yorkie/v1/resources.{proto,pb.go}`, `pkg/document/document_test.go`, 테스트 `pkg/document/zz_skeleton_gc_test.go`, `pkg/document/zz_gc_cost_test.go`, `zz_variant_init_test.go`(2개).

## 3. proto 필드 번호 (최종)

js-sdk main의 `TextNode` 6번(`removal_events`)과 겹치지 않게 맞췄다.

| 메시지 | 필드 | 번호 |
|---|---|---|
| `TextNode` | `id` / `value` / `removed_at` / `ins_prev_id` / `attributes` | 1 / 2 / 3 / 4 / 5 (기존) |
| `TextNode` | (js-sdk `removal_events`, Go에는 없음) | 6 — 사용 안 함 |
| `TextNode` | `has_origin` | 7 |
| `TextNode` | `origin_left` | 8 |
| `TextNode` | `origin_right` | 9 |
| `TextNode` | `origin_flags` | 10 |
| `TextNode` | `skeleton_len` (신규) | 11 |
| `Operation.Edit` | `has_origin` | 11 |
| `Operation.Edit` | `origin_left` | 12 |
| `Operation.Edit` | `origin_right` | 13 |
| `Operation.Edit` | `origin_right_at_from` | 14 |

`Operation.Edit`는 원래 11–14번이었고, js-sdk Edit은 10번까지만 쓰므로 바꾸지 않았다.

## 4. 테스트 결과

| 항목 | 이전 (프로토타입, immediate + 물리 purge) | 이후 (skeleton) |
|---|---|---|
| GC 최소 재현 (XY, d0만 X purge → `abc` / `cab`) | 발산 | `TestZZSkeletonGCMinRepro` 통과(수렴, d0에 X skeleton 1개) |
| `TestZZFuzzMin` GC 포함 20,000 | 발산 11, not found 46 | **발산 0, not found 0** |
| `TestZZFuzzMin` GC 없음 20,000 (immediate) | 0 | 0 |
| `TestZZFuzzMin` GC 없음 20,000 (live, `RO_LIVE=1`) | 138 | 138 (참고용, 기본 경로 아님) |
| `TestZZSkeletonGCFuzz` 20,000 (GC + 스냅샷 재로드, 범위 삭제 포함) | — | 발산 0, 에러 0 (d0에 생긴 skeleton 누적 8,303개) |
| `TestZZMixedOps` undo+gc / old-client+extra+gc, replicas=3 | 에러("right origin not found")로 실패 | 에러 0, 발산 0 (old-client 제외) |
| `TestIntentPropertyInterleaving` (16개 설정 × 300) | 0% | **0%** (모든 설정 0/300) |
| `TestTextNormalizePosMatchesChainWalk` | 통과 | 통과 |
| 스냅샷 왕복 (`TestZZSkeletonSnapshotRoundTrip`) | — | 통과(구조 문자열 동일, GC pair 0) |
| GC 후 undo (`TestZZSkeletonUndoAfterGC`) | — | 통과(skeleton을 제자리에서 다시 채움, 수렴) |
| `go test ./pkg/... ./api/...` | 13개 실패(아래 2개 포함) | **실패 2개만 남음, 둘 다 이 작업 이전부터 있던 것** |

남은 실패 2개(skeleton과 무관하며 WIP 커밋부터 실패):
1. `TestIntentRepro/single_op_words`: "가나다"와 "abc"를 각각 **한 번의 연산**으로 같은 caret에 넣으면 두 삽입의 left/right origin이 똑같다. 그래서 순서는 actor tie-break로만 정해진다. 결과 `X가나다abcY`도 섞이지 않았으므로 의도는 지켜졌다. 테스트가 기대하는 `Xabc가나다Y`는 정보만으로는 고를 수 없는 순서라서 기대값을 고쳐야 한다.
2. `TestForwardOperationsAreByteIdenticalOnTheWire`: Edit 연산에 `has_origin`(field 11)이 실리면서 바이트가 달라졌다. 의도한 포맷 변경이므로 golden 값을 갱신해야 한다.

기존 테스트 중 기대값을 고친 것은 2개다. 둘 다 "GC 후 노드가 사라진다"는 구조를 검사하던 것이라 skeleton 설계에 맞게 고쳤다. `root_test.go`의 GC 테스트 2개는 내용이 있는 노드 수를 세도록, `document_test.go`의 text GC 테스트는 구조 문자열에 `skel(2)`가 들어가도록 바꿨다.

## 5. 비용

### 5.1 10k 문자, 2명 작성 후 X% 삭제 → 양쪽 GC (`ZZ_COST=1 go test -run TestZZGCCost`)
2명이 각자 5,000자를 키 입력 단위로 친다(50타마다 sync). 그다음 범위 삭제(1–20자)를 반복해 X%를 지우고, sync한 뒤 min VV로 GC한다. 측정 대상은 d0이다.

| 삭제 | 남은 live | 노드 수 main | 노드 수 branch | 스냅샷 main (B) | 스냅샷 branch (B) | B/live main | B/live branch | heap/live main | heap/live branch |
|---|---|---|---|---|---|---|---|---|---|
| 0% | 10,000 | 10,000 | 10,000 | 279,928 | 319,982 | 28.0 | 32.0 | 662 | 758 |
| 10% | 9,000 | 9,000 | 10,000 | 251,948 | 318,982 | 28.0 | 35.4 | 683 | 844 |
| 30% | 7,016 | 7,016 | 10,000 | 196,431 | 316,998 | 28.0 | 45.2 | 760 | 1,109 |
| 50% | 5,038 | 5,038 | 10,000 | 141,047 | 315,020 | 28.0 | 62.5 | 923 | 1,629 |

흩어진 1글자 삭제 모드도 거의 같았다(branch 50%: 315,010 B, 62.65 B/live).

- 0%에서도 14% 큰 것은 origin 필드 비용이다(프로토타입 자체 비용, skeleton과 무관).
- skeleton 하나는 스냅샷에서 약 32B로, 1글자짜리 live 노드와 비슷하다. removed_at을 빼기 전에는 약 52B였고, 그때 50% 삭제 스냅샷은 419,222 B(83.2 B/live)였다.
- **키 입력 단위 작성에서는 글자마다 insertion이 달라 병합이 일어나지 않는다.** 그래서 노드 수가 지금까지 친 글자 수(10,000)로 고정된다. 병합은 한 번에 붙여 넣은 텍스트처럼 한 insertion을 여러 번 나눠 지운 경우에만 효과가 있다.
- heap은 skeleton 하나가 대략 노드 하나(ID, 티켓, splay/LLRB 엔트리, origin 포인터)만큼 든다.

### 5.2 벤치마크 (Apple M4, main과 branch를 1회씩 번갈아 10번, benchstat)

| 벤치마크 | main | branch | 차이 |
|---|---|---|---|
| TextEditing (editing trace) sec/op | 3.504 ± 8% | 3.849 ± 23% | ~ (p=0.052, 유의하지 않음) |
| TextEditing B/op | 3.775 GiB | 3.799 GiB | +0.64% |
| TextEditing allocs/op | 13.42M | 13.60M | +1.36% |
| Document/text_edit_gc_100 | 868 µs | 838 µs | ~ |
| Document/text_edit_gc_1000 | 43.1 ms | 43.6 ms | ~ |
| Document/text_split_gc_100 | 1.47 ms | 1.55 ms | ~ |
| Document/text_split_gc_1000 | 69.9 ms | 70.9 ms | ~ |
| geomean (시간) | | | +2.8% (유의하지 않음) |

측정 노이즈가 커서(±8–43%) 시간 차이는 유의하지 않다. allocs/B는 origin 기록 때문에 1–3% 늘었다. 원시 결과: `gc-skeleton-bench/bm_main.txt`, `gc-skeleton-bench/bm_branch.txt`.

## 6. 남은 문제

1. **skeleton이 영원히 남는다.** 삭제가 많은 문서일수록 노드 수와 heap이 지금까지 친 글자 수에 비례한다(50% 삭제 시 B/live 2.2배, heap/live 1.8배). 줄이는 방법은 두 가지다.
   - Yjs처럼 **같은 actor의 연속된 insertion을 하나의 범위 skeleton으로 합친다.** Yorkie ID는 `(ticket, offset)`이라 lamport가 연속되지 않을 수 있다. ID 조회를 범위 단위로 하도록 treeByID를 바꿔야 하는 설계 변경이다.
   - **더 이상 참조될 수 없는 skeleton을 실제로 지운다.** 그런데 immediate 규칙에서는 새 로컬 삽입이 skeleton을 right origin으로 가리킬 수 있다. "언제 안전하게 지울 수 있는가"를 증명해야 하므로 손대지 않았다.
2. **js-sdk 미구현.** Go만 skeleton과 `skeleton_len`(11번)을 안다. js-sdk에도 같은 skeleton 규칙과 필드 번호(7–11, Edit 11–14)를 반영해야 한다. 지금 상태로 Go 서버가 만든 스냅샷을 js-sdk가 읽으면 skeleton을 길이 0의 이상한 노드로 해석하게 된다.
3. **old client(origin 없는 클라이언트) 혼합 발산** (`TestZZMixedOps` one-old-client: 300건 중 57–97건). 이번 작업 이전부터 있었고, GC와 별개인 호환성 문제다.
4. **기대값/golden 갱신 필요** 2건(4절 참고).
5. undo 복원 시 속성: GC된 replica는 span 값(삭제 시점 속성)으로, GC 안 된 replica는 tombstone 속성으로 복원한다. 삭제 후 동시에 들어온 style이 있으면 속성이 달라질 수 있다. 다만 main의 purge 후 재생성 경로와 같은 성격이고 새로 생긴 문제는 아니다.
6. `Root.collect`가 Text 노드만 purge 전에 크기를 읽도록 타입 확인(`IsSkeleton`)을 한다. 다른 타입의 동작은 바꾸지 않았지만, 깔끔하게 하려면 GC 인터페이스에 "purge 전 크기"를 두는 편이 낫다.
