package store

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tranbaohieu/lawliet/internal/index"
)

// fakeClock is a manually advanced clock in unix ms.
type fakeClock struct{ ms atomic.Int64 }

func (f *fakeClock) now() int64              { return f.ms.Load() }
func (f *fakeClock) advance(d time.Duration) { f.ms.Add(d.Milliseconds()) }

func newTTLCollection(t *testing.T) (*Store, *Collection, *fakeClock) {
	t.Helper()
	clk := &fakeClock{}
	clk.ms.Store(1_000_000)
	s := New()
	s.SetClock(clk.now)
	return s, mustCreate(t, s, "c", 1, index.IP), clk
}

func TestExpiredRecordIsInvisible(t *testing.T) {
	_, c, clk := newTTLCollection(t)
	c.Add("a", Record{Vector: []float32{1}, ExpireAt: clk.now() + 1000}, Upsert)
	c.Add("b", Record{Vector: []float32{2}}, Upsert)

	if !c.Exists("a") {
		t.Fatal("a should exist before expiry")
	}
	clk.advance(999 * time.Millisecond)
	if !c.Exists("a") {
		t.Fatal("a should exist 1ms before expiry")
	}
	clk.advance(time.Millisecond) // exactly at ExpireAt: expired

	if c.Exists("a") {
		t.Fatal("Exists: expired record visible")
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("Get: expired record visible")
	}
	res, _ := c.Search([]float32{1}, SearchOptions{K: 10})
	if len(res) != 1 || res[0].ID != "b" {
		t.Fatalf("Search: %+v", res)
	}
	// Lazy reclaim on access freed the slot.
	if info := c.Info(); info.Count != 1 || info.Expires != 0 {
		t.Fatalf("after lazy reclaim: %+v", info)
	}
}

func TestSearchReclaimsExpired(t *testing.T) {
	_, c, clk := newTTLCollection(t)
	for i := 0; i < 5; i++ {
		c.Add(fmt.Sprint(i), Record{Vector: []float32{float32(i)}, ExpireAt: clk.now() + 10}, Upsert)
	}
	c.Add("keep", Record{Vector: []float32{1}}, Upsert)
	clk.advance(time.Second)

	if c.Count() != 6 {
		t.Fatalf("expired records are only reclaimed on access: count %d", c.Count())
	}
	res, _ := c.Search([]float32{1}, SearchOptions{K: 10})
	if len(res) != 1 || res[0].ID != "keep" {
		t.Fatalf("Search: %+v", res)
	}
	if c.Count() != 1 {
		t.Fatalf("Search should reclaim expired records, count %d", c.Count())
	}
}

func TestTTLExpirePersist(t *testing.T) {
	_, c, clk := newTTLCollection(t)
	c.Add("a", Record{Vector: []float32{1}}, Upsert)

	if _, st := c.TTL("a"); st != TTLNone {
		t.Fatalf("TTL without expiry: %v", st)
	}
	if _, st := c.TTL("missing"); st != TTLNotFound {
		t.Fatalf("TTL missing: %v", st)
	}
	if c.Persist("a") {
		t.Fatal("Persist without TTL should return false")
	}

	if !c.Expire("a", clk.now()+5000) {
		t.Fatal("Expire on existing record should return true")
	}
	if ms, st := c.TTL("a"); st != TTLSet || ms != 5000 {
		t.Fatalf("TTL: %d %v", ms, st)
	}
	if !c.Persist("a") {
		t.Fatal("Persist should remove TTL")
	}
	if _, st := c.TTL("a"); st != TTLNone {
		t.Fatalf("after Persist: %v", st)
	}
	if c.Expire("missing", clk.now()+1000) {
		t.Fatal("Expire on missing record should return false")
	}

	// Expire with a time in the past deletes immediately.
	if !c.Expire("a", clk.now()) || c.Exists("a") || c.Count() != 0 {
		t.Fatal("Expire in the past should delete the record")
	}
}

func TestWritesTreatExpiredAsAbsent(t *testing.T) {
	_, c, clk := newTTLCollection(t)
	c.Add("a", Record{Vector: []float32{1}, Text: "old", ExpireAt: clk.now() + 10}, Upsert)
	clk.advance(time.Second)

	if r, _ := c.Add("a", Record{Vector: []float32{2}}, OnlyExisting); r != Skipped {
		t.Fatalf("XX on expired: %v", r)
	}
	c.Add("b", Record{Vector: []float32{1}, ExpireAt: clk.now() + 10}, Upsert)
	clk.advance(time.Second)
	if r, _ := c.Add("b", Record{Vector: []float32{3}}, OnlyNew); r != Inserted {
		t.Fatalf("NX on expired: %v", r)
	}
	if rec, ok := c.Get("b"); !ok || rec.Vector[0] != 3 || rec.ExpireAt != 0 {
		t.Fatalf("Get b: %+v", rec)
	}
	if n := c.Delete("a"); n != 0 {
		t.Fatalf("Delete of expired record should count 0, got %d", n)
	}
	if c.Persist("a") || c.Expire("a", clk.now()+1000) {
		t.Fatal("TTL commands on expired record should behave as missing")
	}
	if c.Count() != 1 {
		t.Fatalf("count %d", c.Count())
	}
}

func TestUpsertClearsTTL(t *testing.T) {
	_, c, clk := newTTLCollection(t)
	c.Add("a", Record{Vector: []float32{1}, ExpireAt: clk.now() + 1000}, Upsert)
	c.Add("a", Record{Vector: []float32{1}}, Upsert)
	if _, st := c.TTL("a"); st != TTLNone {
		t.Fatalf("upsert without TTL should clear it: %v", st)
	}
	if c.Info().Expires != 0 {
		t.Fatal("ttlSlots not cleaned")
	}
}

func TestExpireCycle(t *testing.T) {
	s, c, clk := newTTLCollection(t)
	c2 := mustCreate(t, s, "c2", 1, index.IP)

	for i := 0; i < 500; i++ {
		c.Add(fmt.Sprintf("short%d", i), Record{Vector: []float32{1}, ExpireAt: clk.now() + 10}, Upsert)
		c2.Add(fmt.Sprintf("short%d", i), Record{Vector: []float32{1}, ExpireAt: clk.now() + 10}, Upsert)
	}
	for i := 0; i < 100; i++ {
		c.Add(fmt.Sprintf("long%d", i), Record{Vector: []float32{1}, ExpireAt: clk.now() + 3_600_000}, Upsert)
		c.Add(fmt.Sprintf("none%d", i), Record{Vector: []float32{1}}, Upsert)
	}
	clk.advance(time.Second)

	// One cycle repeats rounds while > 25% of a sample is expired, so it removes
	// most expired records but may stop with a small residue (same as Redis).
	removed := s.ExpireCycle(time.Now().Add(time.Second))
	if removed < 300 {
		t.Fatalf("first cycle removed %d, want a large share of the 1000 expired records", removed)
	}
	// Later cycles (one per tick in production) reclaim the residue.
	cycles := 1
	for ; cycles < 1000 && (c.Count() != 200 || c2.Count() != 0); cycles++ {
		s.ExpireCycle(time.Now().Add(time.Second))
	}
	if c.Count() != 200 || c2.Count() != 0 {
		t.Fatalf("counts after %d cycles: c=%d c2=%d", cycles, c.Count(), c2.Count())
	}
	t.Logf("first cycle removed %d/1000, all reclaimed after %d cycles", removed, cycles)
	if c.Info().Expires != 100 {
		t.Fatalf("long-TTL records should remain: %d", c.Info().Expires)
	}
}

func TestExpireCycleStopsWhenFewExpired(t *testing.T) {
	s, c, clk := newTTLCollection(t)
	for i := 0; i < 1000; i++ {
		c.Add(fmt.Sprint(i), Record{Vector: []float32{1}, ExpireAt: clk.now() + 3_600_000}, Upsert)
	}
	if n := s.ExpireCycle(time.Now().Add(time.Second)); n != 0 {
		t.Fatalf("nothing is expired, removed %d", n)
	}
}

func TestConcurrentTTL(t *testing.T) {
	s, c, clk := newTTLCollection(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() { // background clock and expirer
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				clk.advance(time.Millisecond)
				s.ExpireCycle(time.Now().Add(time.Millisecond))
			}
		}
	}()
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				id := fmt.Sprintf("%d-%d", w, i%20)
				c.Add(id, Record{Vector: []float32{1}, ExpireAt: clk.now() + int64(i%5)}, Upsert)
				c.Search([]float32{1}, SearchOptions{K: 5})
				c.Get(id)
				c.TTL(id)
				if i%3 == 0 {
					c.Persist(id)
				}
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.live != len(c.idToSlot) {
		t.Fatalf("live %d != map %d", c.live, len(c.idToSlot))
	}
	for slot := range c.ttlSlots {
		if c.deleted[slot] || c.expireAt[slot] == 0 {
			t.Fatalf("ttlSlots inconsistent at slot %d", slot)
		}
	}
}
