package index

import "sort"

// topK keeps the k highest-scoring hits using a min-heap:
// the root is the worst hit kept so far, so a new hit only needs
// to beat the root to get in.
type topK struct {
	k int
	h []Hit
}

func newTopK(k int) *topK {
	c := k
	if c > 1024 {
		c = 1024 // avoid a huge allocation for large K on small collections
	}
	return &topK{k: k, h: make([]Hit, 0, c)}
}

// worse reports whether a ranks below b. Ties are broken by slot so results are deterministic.
func worse(a, b Hit) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.Slot > b.Slot
}

func (t *topK) push(x Hit) {
	if len(t.h) < t.k {
		t.h = append(t.h, x)
		t.up(len(t.h) - 1)
		return
	}
	if worse(t.h[0], x) {
		t.h[0] = x
		t.down(0)
	}
}

func (t *topK) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !worse(t.h[i], t.h[p]) {
			return
		}
		t.h[i], t.h[p] = t.h[p], t.h[i]
		i = p
	}
}

func (t *topK) down(i int) {
	n := len(t.h)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		m := l
		if r := l + 1; r < n && worse(t.h[r], t.h[l]) {
			m = r
		}
		if !worse(t.h[m], t.h[i]) {
			return
		}
		t.h[i], t.h[m] = t.h[m], t.h[i]
		i = m
	}
}

// sorted returns the kept hits, best first.
func (t *topK) sorted() []Hit {
	out := t.h
	sort.Slice(out, func(i, j int) bool { return worse(out[j], out[i]) })
	return out
}
