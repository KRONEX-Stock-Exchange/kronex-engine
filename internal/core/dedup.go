package core

type recentIDs struct {
	seen map[int64]struct{} // 사용 여부 조회
	ring []int64            // 적재 순서 보관
	pos  int                // 다음에 쓰일 ring 인덱스
}

func newRecentIDs(window int) *recentIDs {
	if window <= 0 {
		window = 1
	}
	return &recentIDs{
		seen: make(map[int64]struct{}, window),
		ring: make([]int64, window),
	}
}

// ID 사용 여부 반환
func (r *recentIDs) has(id int64) bool {
	_, ok := r.seen[id]
	return ok
}

// ID 기록 및 가장 오래된 ID 추출
func (r *recentIDs) add(id int64) {
	if _, ok := r.seen[id]; ok {
		return
	}

	if old := r.ring[r.pos]; old != 0 {
		delete(r.seen, old)
	}
	r.ring[r.pos] = id
	r.seen[id] = struct{}{}
	r.pos++
	if r.pos == len(r.ring) {
		r.pos = 0
	}
}

type dedup struct {
	window int                   // 최대 저장 크기
	byKind map[string]*recentIDs // 종류별 사용 여부를 저장
}

func newDedup(window int) *dedup {
	return &dedup{window: window, byKind: make(map[string]*recentIDs)}
}

// ID 사용 여부 조회
func (d *dedup) has(kind string, id int64) bool {
	r := d.byKind[kind]
	return r != nil && r.has(id)
}

// ID 사용 처리
func (d *dedup) add(kind string, id int64) {
	r := d.byKind[kind]
	if r == nil {
		r = newRecentIDs(d.window)
		d.byKind[kind] = r
	}
	r.add(id)
}
