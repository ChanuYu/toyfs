# toyfs — Crash Recovery

이 문서는 `toyfs`의 **crash 시뮬레이션**과 **복구 시나리오**를 한 곳에 정리한다.
06번 block cache, 07번 journal, 08번 snapshot에 흩어진 crash 관련 내용을
시나리오 단위로 묶어 *실제 구현/테스트 시 따라갈 절차*로 제시.

핵심 원칙:
- **저널링이 보장하는 것**: 사용자에게 ack한 메타데이터 변경은 *완전히* 살아남거나 *완전히* 안 일어난 상태
- **ordered 모드의 추가 보장**: 메타데이터가 가리키는 데이터는 valid (garbage 노출 없음)
- **시뮬레이션은 두 가지**: 디스크 이미지 cp 시점 보존 (현실적 X), block cache fault injection (현실적 O)

## 1. crash가 만들 수 있는 상태

journaling FS 관점에서 가능한 crash 시점:

```
타임라인:                    결과:
1. 메타데이터 cache에 변경     → 무시 (디스크에 미반영)
2. 데이터 본위치 flush 시작    → 일부 본위치 갱신, 메타는 옛 값
3. 데이터 본위치 flush 완료    → 본위치 새 값, 메타는 아직 옛 값
4. journal descriptor write    → 미완 transaction (commit 없음)
5. journal metadata write      → 미완 transaction
6. journal revocation write    → 미완 transaction
7. journal commit write 시작   → 부분 commit record (checksum 깨짐)
8. journal commit write 완료   → 완전 transaction. replay 대상
9. journal sync 완료            → 사용자 ack 가능 시점
10. 메타 본위치 flush 일부      → checkpoint 일부. journal에 사본 있음
11. 메타 본위치 flush 완료     → checkpoint 완료
12. journal tail 갱신           → journal 영역 재사용 가능
```

각 시점에서 crash 시 mount 시 어떻게 보여야 하는가:

| crash 시점 | mount 후 결과 |
|---|---|
| 1~3 | 메타 변경 안 일어남 (= 사용자 ack 안 한 변경 사라짐) |
| 4~7 | 미완 transaction → replay에서 무시. 결과는 1~3과 동일 |
| 8~11 | 완전 transaction → replay로 본위치까지 적용. 사용자 ack한 그대로 살아남음 |
| 12 | 정상 상태와 동일 |

핵심: **사용자 ack 시점이 8 (commit record sync 완료) 직후**. 그 전 crash는 ack 안 했으니 사라져도 OK, 그 후 crash는 살아남아야 함.

## 2. crash 시뮬레이션 방법

### 2.1 방법 (i): 디스크 이미지 시점 cp

가장 단순:
```
$ cp disk.img disk.img.checkpoint    # 임의 시점에 사본
# 이후 toyfs ops 계속...
$ ./toyfs mount disk.img.checkpoint /mnt    # 시점 사본을 mount
```

장점:
- 단순. 외부 도구만 사용
- 디스크 상태가 *실제로 그 시점*인 것이 보장됨

단점:
- crash가 "이 시점에 일어났다"의 정밀도가 cp 호출 시점에 의존
- mount 중 cp는 cache에 dirty가 남아있어 디스크와 view가 다를 수 있음 → 정확히는 fsync + cp 후 다음 ops 진행

### 2.2 방법 (ii): block cache fault injection

코드 안에서 시뮬레이션:
```
type FaultMode int
const (
    FaultNone FaultMode = iota
    FaultDropFutureWrites    // 이 시점 이후 BlockDevice.Write 호출이 무시됨
    FaultPartialWrite         // 다음 N개 write만 허용
)

func (d *BlockDevice) SetFault(mode FaultMode, n int) { ... }
```

테스트가 임의 시점에 `SetFault(FaultDropFutureWrites)` 호출 → 그 이후의 모든 디스크 write가 *디스크에 닿지 않음*. cache는 그대로지만 디스크는 옛 상태.

이 상태에서:
1. 프로세스 종료 (cache 통째 버림)
2. 다시 mount → journal replay 발동

장점:
- crash 시점을 *코드 한 줄로* 정밀 제어
- 테스트 자동화 가능
- 실제 crash와 의미적으로 동일 (cache는 휘발, 디스크는 그 시점까지의 write만 반영)

단점:
- 코드에 hook 필요 (BlockDevice.Write에 fault check)

### 2.3 우리의 채택

**둘 다 사용.** 5단계 초기에는 (i)로 시작 (코드 변경 없이 시작), 본격 테스트는 (ii)로 자동화.

## 3. mount 시 정상/비정상 판정

02번 4절 정리한 mount 흐름:

```
func Mount(image_path):
    sb := readSuperblock(image_path)             // 블록 1
    if sb.magic != "TOYF":                        return EIO
    if sb.checksum 검증 실패:
        sb := readBackupSuperblock(image_path)    // 마지막 블록
        if 그것도 실패:                            return EIO
    if sb.flags & TOYFS_SB_CLEAN == 0:
        // 비정상 unmount → journal replay
        replayJournal(image_path, sb)
    sb.flags &= ~TOYFS_SB_CLEAN     // 이제 mount 중
    writeSuperblock(image_path, sb)
    sync()
    initInMemoryStructures(sb)
    return OK
```

`TOYFS_SB_CLEAN` 비트:
- mount 시 0으로 set
- unmount 시 1로 set (모든 dirty flush + journal checkpoint 완료 후)

CLEAN 비트가 1 = 정상 unmount된 디스크. CLEAN이 0 = mount 중이거나 비정상 종료.

## 4. journal replay (mount 시)

07번 9절 4-pass 알고리즘의 시각화:

```
Pass 1: scan        → 완전 transaction 목록 작성
Pass 2: revocation  → revoked block 테이블
Pass 3: replay      → 본위치에 metadata write (escape 복원 포함, revoked skip)
Pass 4: 마무리       → journal head/tail 클리어, fs_sb CLEAN set
```

각 pass의 실패 처리:

- Pass 1에서 commit record 못 찾으면 그 transaction 무시. 정상 (= 미완 transaction은 사라지는 것이 옳음)
- Pass 3에서 본위치 write 실패 → fatal. mount 실패 (디스크 손상 의심)
- Pass 4 sync 실패 → fatal

## 5. 시나리오별 crash + 복구

각 ops에 대해 crash 가능 시점과 복구 결과를 정리.

### 5.1 mkdir

mkdir가 일으키는 메타데이터 변경 (5번/10번 절):
1. 새 inode 할당 (inode_bitmap, inode_table 슬롯)
2. 새 디렉토리 데이터 블록 할당 (block_bitmap, 데이터 블록에 `.`/`..` write)
3. 부모 디렉토리에 새 dentry 추가 (부모 데이터 블록)
4. 부모 inode의 nlink/mtime/ctime 갱신

crash 시점별:

| crash 시점 | mount 후 |
|---|---|
| (a) journal에 미완 — descriptor만 write | mkdir 안 일어남. inode/블록 모두 free 상태 (= 옛 bitmap 그대로) |
| (b) journal에 미완 — metadata write 중 | 동일 (a) |
| (c) journal commit 직전 | 동일 (a) |
| (d) journal commit 직후 | mkdir 완전히 일어남. replay로 본위치 4개 갱신 |
| (e) checkpoint 일부 | 일부 본위치는 새 값, 일부는 옛 값. replay로 *옛 값을 새 값으로 정정* |
| (f) checkpoint 완료 후 | 정상 상태 |

검증: 각 시점에서 mount 후
- (a)~(c): `ls parent`에 새 디렉토리 안 보임. inode_bitmap에 그 비트 0. 정합
- (d)~(f): `ls parent`에 새 디렉토리 보임, `ls newdir`에 `.`/`..` 정상

### 5.2 unlink

ops가 일으키는 변경:
1. 부모 dentry tombstone (= dentry.inode = 0)
2. target inode의 nlink--
3. nlink == 0 + 다른 조건 만족 시 inode + 데이터 블록 free
4. (revocation 추가 — inode가 가리키던 메타데이터 블록이었다면)

crash 시점별:

| 시점 | 결과 |
|---|---|
| commit 전 | unlink 안 일어남 (옛 dentry 그대로) |
| commit 후 | unlink 완전히 일어남 |
| inode free 후 free 처리 도중 | 한 transaction이라 atomic. 부분 free 없음 |

데이터 블록 free 시점에 *그 블록이 미래에 일반 파일 데이터로 재할당*되는 시나리오 → revocation 필요 (07번 8절).

### 5.3 rename across dirs

가장 복잡한 ops. 5번 9절:
1. source dir에서 entry tombstone
2. target dir에 entry insert
3. target이 디렉토리면 자식의 `..` 갱신
4. source/target dir의 nlink 변동

위 1~4가 한 transaction. crash 시:
- commit 전: 두 dir 모두 옛 상태 (rename 안 일어남)
- commit 후: 새 상태 완전. target dir에 entry, source에는 없음, 자식 `..`도 새 inode

만약 trans의 메타데이터 블록 수가 203개를 넘으면 한 transaction에 안 들어감 → 1단계 ops가 그렇게 큰 변경을 만들지 않도록 보장 (보통 ~10블록 이하).

### 5.4 write (데이터 변경)

ordered 모드에서 write가 만드는 변경:
1. 새 데이터 블록 할당 (필요 시) — block_bitmap 갱신
2. 본위치에 데이터 write (cache → 본위치)
3. inode의 size/mtime/ctime/extent 갱신

ordered 강제 (07번 5.3 Phase 1): 메타데이터 commit 시작 전 데이터가 본위치 도달.

crash 시점별:

| 시점 | 결과 |
|---|---|
| 데이터 본위치 flush 도중 | 메타 transaction commit 전이므로 → mount 후 옛 inode 상태. 본위치에 일부 새 데이터 있어도 어떤 inode도 안 가리킴 → 의미 없음. orphan 블록은 fsck로 회수 (1단계 범위 밖) |
| 데이터 flush 완료 + 메타 commit 전 | 동일 (옛 inode 상태) |
| 메타 commit 후 | inode가 새 데이터 블록 가리킴 + 그 블록은 *반드시* 새 데이터 (ordered 보장) |
| checkpoint 후 | 정상 |

**핵심**: ordered 덕분에 "inode는 새 블록 가리키는데 그 블록은 옛 데이터" 시나리오 *없음*.

### 5.5 snapshot create

08번 6절 절차. 한 transaction:
1. snapshot_meta에 bitmap/inode_table 사본 write
2. refcount table 일괄 갱신
3. 모든 extent의 SHARED flag set
4. superblock의 snapshot 슬롯 등록

crash 시점:
- commit 전: snapshot 안 만들어짐. 모든 자료구조 옛 값
- commit 후: 완전한 snapshot. replay로 본위치 사본 + refcount + SHARED + 슬롯 모두 갱신

(2)의 refcount 일괄 갱신은 메타데이터 블록 ~8개 갱신. (3)은 활성 inode 수만큼 inode_table 블록 수정. 한 ops에서 *수십 ~수백 메타데이터 블록 수정*이 되어 transaction 한도 203에 근접 가능. 1단계 정책: snapshot create 시 단일 transaction 한도 초과하면 *작은 단위로 chunk* 또는 transaction 한도를 임시 확장. 자세한 구현은 5단계.

### 5.6 snapshot delete

08번 8절. 한 transaction:
1. snapshot이 가리키던 모든 블록 refcount--
2. refcount 0이 된 블록은 block_bitmap에서 0
3. snapshot_meta 슬롯 클리어
4. superblock 슬롯 비움

crash 시점은 create와 같은 모델. 단일 transaction이라 부분 상태 없음.

### 5.7 snapshot rollback

08번 9절. 가장 큰 단일 transaction. 단순화 정책 (3 제약: open file 없음, 다른 ops 없음, 다른 snapshot 0개):

```
1. journal flush + checkpoint 완료 대기 (이전 모든 transaction 디스크 도달)
2. 메타데이터 사본 통째 복원 (bitmap × 2 + inode_table)
3. data block refcount/bitmap 정리
4. extent SHARED flag 클리어 (활성 inode 전체 traversal)
5. snapshot 슬롯 클리어
```

**중요**: 이 모든 단계가 한 transaction. journal에 *통째 사본 복원*을 metadata record로 묶어 기록. 부분 상태 없음.

crash 시점:
- 1단계 도중: 정상 종료처럼 처리. rollback 안 시작. 다음 mount 시 정상
- 2~5단계 commit 전: rollback transaction 미완 → replay에서 무시 → 활성 FS는 *rollback 시도 전 상태*로 복귀
- commit 후: rollback 완전히 일어남 → replay로 모든 본위치 갱신

→ rollback 시도 자체가 *완전히 일어났거나 안 일어났거나*. 부분 상태 노출 없음.

> rollback transaction의 메타데이터 블록 수: bitmap (2) + inode_table (256) + extent SHARED 클리어 (활성 inode 수에 비례, 최대 inode_table 블록 수 256) + 슬롯 메타 (1) ≈ 515 블록. **203 한도 초과**.
> 1단계 단순화로 rollback은 *여러 transaction에 걸쳐 진행되도록* 분할:
> - txn 1: bitmap 복원 + 슬롯 클리어
> - txn 2 ~ N: inode_table 256 블록을 chunk 단위로 복원
> - txn N+1: refcount 정리 + SHARED 클리어
> 단, 분할되면 *중간 상태가 디스크에 보임*. 이 시점에 mount하면 일부만 복원된 상태.
>
> 해결: rollback 진행 표시를 superblock에 special flag로 둠. crash 후 mount 시 이 flag가 set이면 *rollback이 미완료된 상태*로 인식 → rollback 재개 또는 중단.

### 5.7.1 rollback 진행 flag

superblock의 `flags`에 추가:
```
TOYFS_SB_ROLLBACK_IN_PROGRESS = 1 << 4
```

mount 시 이 flag가 set이면:
1. 진행 중인 rollback의 대상 snapshot ID를 superblock에서 읽음 (별도 필드 필요)
2. rollback 재개: 어디까지 진행됐는지 *복구 가능한 메타데이터 상태에 기반해* 결정
3. 단순화 옵션: rollback 재개 대신 *해당 snapshot으로의 rollback을 처음부터 다시 수행*

> 이 부분은 1단계 학습 가치가 크지만 구현 부담도 큼. **제안**: rollback 자체를 *atomic 단일 transaction으로 강제*하기 위해 transaction 한도를 일시적으로 확장하는 path를 두자. 즉 rollback 전용 commit 함수가 203 한도 무시하고 *필요한 만큼* descriptor를 여러 개 사용. 자세한 구현은 5단계에서 결정.

복잡도가 보입니다. **이 결정은 5단계 구현 시점에 다시 검토.** 1단계 설계 문서에서는 *문제만 짚어두고* 두 가지 대안(재개 flag / 한도 확장)을 명시.

## 6. fsck — 1단계 범위 밖

본격 fsck는 1단계에서 다루지 않지만, 다음 검증 도구는 학습 가치 큼:

```
toyfs-fsck disk.img
  - superblock magic / checksum 검증
  - bitmap consistency: bitmap의 set count == 사용 중 inode/block 수
  - inode invariant: nlink가 dentry 수와 일치
  - extent invariant: 04번 6절의 7가지
  - refcount invariant: refcount_table[B] > 0 ↔ 활성 inode 또는 snapshot이 B를 가리킴
  - dentry invariant: 05번 11절
```

읽기 전용 검증 도구로 시작 → 5단계 이후 *자동 정정* 추가.

## 7. crash recovery testing 시나리오

### 7.1 시나리오 그룹 A — 단일 ops crash

각 핵심 ops에 대해:
1. ops 시작
2. 임의 시점에 fault injection
3. 프로세스 종료
4. 재mount
5. 결과 검증 (commit 전이면 안 일어남, 후면 일어남)

대상 ops: create / mkdir / unlink / rmdir / rename / write / chmod / link / symlink

### 7.2 시나리오 그룹 B — 누적 ops crash

여러 ops를 순차 실행 후 임의 시점에 crash:
1. mkdir A; touch A/f1; touch A/f2; write A/f1 ...
2. fault injection at random
3. unmount + remount
4. journal replay 후 *어디까지* 일어났는지 확인. 사용자 ack 시점 (commit 후) 기준으로 일관 분기

### 7.3 시나리오 그룹 C — snapshot 관련 crash

- snapshot create 도중 crash → snapshot 미생성 또는 완전 생성
- snapshot delete 도중 crash → 동일
- rollback 도중 crash → 5.7 시나리오대로

### 7.4 시나리오 그룹 D — replay 자체의 crash

replay 도중 또 crash:
1. crash 후 mount → replay 시작
2. replay Pass 3 도중 또 crash (fault injection)
3. 다시 mount → replay 재시도 (replay는 idempotent)
4. 정상 완료

replay가 idempotent해야 한다는 invariant를 이 시나리오로 검증.

## 8. unmount의 역할

정상 unmount는 다음을 보장:
1. 진행 중인 모든 transaction commit + checkpoint 완료
2. 모든 dirty cache 슬롯 본위치 flush
3. journal head/tail 정리 (모두 비어있는 상태)
4. superblock의 CLEAN flag set
5. backup superblock도 갱신
6. fsync 후 close

이 절차가 끝나야 디스크 이미지가 *재mount 시 replay 없이* 정상 mount 가능. unmount 도중 crash는 다음 mount에서 replay로 처리.

## 9. invariant — crash recovery 관점

mount 후 (정상 또는 replay 후) 항상:

1. fs_sb의 CLEAN flag = 1 (mount 중에는 0이지만 mount 절차 완료 후)
2. journal head/tail이 정합
3. 모든 inode의 self-checksum 정합
4. block_bitmap의 set count == 사용 중인 데이터 블록 수
5. refcount_table 정합 (08번 invariant 4, 5)
6. extent invariant (04번 6절) 모든 inode에 대해
7. dentry invariant (05번 11절)
8. 모든 transaction이 commit 완료된 상태이거나 replay로 적용 완료

## 10. 요약 — 사용자가 받는 보장

1. **사용자 ack한 (= fsync 응답 받은) 변경은 반드시 살아남는다**
2. **부분 갱신 상태는 사용자에게 노출되지 않는다**
3. **메타데이터가 가리키는 데이터는 valid (ordered 보장)**
4. **여러 ops 간 ordering은 commit 순서대로 reflect됨**

이 4가지가 toyfs의 crash 보장. ext4 ordered 모드와 사실상 동일.

## 11. 다음 문서로의 안내

- `11-roadmap.md` — crash recovery 작업이 5단계의 어떤 작업으로 분해되는지
