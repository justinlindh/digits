package household

import (
	"context"
	"errors"
	"testing"
)

// countStub records how many times it was invoked so tests can assert that the
// cache short-circuits the (real) COUNT query.
type countStub struct {
	calls int
	n     int
	err   error
}

func (c *countStub) count(context.Context, string) (int, error) {
	c.calls++
	return c.n, c.err
}

func TestNeedsOnboarding_NewUserStillGated(t *testing.T) {
	s := &Store{}
	stub := &countStub{n: 0}

	if !s.needsOnboarding(context.Background(), "new-user", stub.count) {
		t.Fatal("a user with no household should need onboarding")
	}
	if stub.calls != 1 {
		t.Fatalf("expected the count query to run once for an uncached user, ran %d times", stub.calls)
	}
	// A negative result is never cached, so a genuinely new user keeps being
	// re-evaluated on every request until they join or create a household.
	if !s.needsOnboarding(context.Background(), "new-user", stub.count) {
		t.Fatal("new user should still be gated on the second call")
	}
	if stub.calls != 2 {
		t.Fatalf("expected the count query to run again for a still-new user, ran %d times total", stub.calls)
	}
}

func TestNeedsOnboarding_ExistingUserSkipsQuery(t *testing.T) {
	s := &Store{}
	stub := &countStub{n: 1}

	// First call misses the cache, queries, finds a household, and caches it.
	if s.needsOnboarding(context.Background(), "member", stub.count) {
		t.Fatal("a user with a household should not need onboarding")
	}
	if stub.calls != 1 {
		t.Fatalf("expected exactly one query on the first (miss) call, got %d", stub.calls)
	}

	// Subsequent calls resolve from cache without touching the counter.
	for i := 0; i < 5; i++ {
		if s.needsOnboarding(context.Background(), "member", stub.count) {
			t.Fatal("cached member should not need onboarding")
		}
	}
	if stub.calls != 1 {
		t.Fatalf("expected the query to run only once, ran %d times", stub.calls)
	}
}

func TestNeedsOnboarding_MarkHasHouseholdSkipsQuery(t *testing.T) {
	s := &Store{}
	stub := &countStub{n: 0} // would report "no household" if ever queried

	// Create and AddMember call markHasHousehold on success; simulate that.
	s.markHasHousehold("joiner")

	if s.needsOnboarding(context.Background(), "joiner", stub.count) {
		t.Fatal("a user who just created or joined a household should not need onboarding")
	}
	if stub.calls != 0 {
		t.Fatalf("expected no query after markHasHousehold, ran %d times", stub.calls)
	}
}

func TestNeedsOnboarding_ForgetHouseholdReArms(t *testing.T) {
	s := &Store{}
	stub := &countStub{n: 1}

	// Prime the cache via a miss.
	if s.needsOnboarding(context.Background(), "leaver", stub.count) {
		t.Fatal("member should not need onboarding")
	}
	if stub.calls != 1 {
		t.Fatalf("expected one priming query, got %d", stub.calls)
	}

	// Leaving the household evicts the cache entry.
	s.forgetHousehold("leaver")

	// The next check must re-query. With the household now gone the counter
	// reports zero, so the user is correctly re-gated.
	stub.n = 0
	if !s.needsOnboarding(context.Background(), "leaver", stub.count) {
		t.Fatal("a user who left their only household should be re-gated")
	}
	if stub.calls != 2 {
		t.Fatalf("expected the query to run again after eviction, ran %d times", stub.calls)
	}
}

func TestNeedsOnboarding_ForgetHouseholdEvictsEachMember(t *testing.T) {
	s := &Store{}
	members := []string{"alice", "bob", "carol"}

	// A shared household caches every member as "has a household".
	for _, id := range members {
		s.markHasHousehold(id)
	}
	for _, id := range members {
		if _, cached := s.hasHousehold.Load(id); !cached {
			t.Fatalf("%s should be cached after markHasHousehold", id)
		}
	}

	// Deleting the household evicts each member (Delete loops forgetHousehold
	// over the deleted rows); every entry must be gone so no member sails past
	// onboarding on a stale positive.
	for _, id := range members {
		s.forgetHousehold(id)
	}
	for _, id := range members {
		if _, cached := s.hasHousehold.Load(id); cached {
			t.Errorf("%s should be evicted after forgetHousehold", id)
		}
	}
}

func TestNeedsOnboarding_QueryErrorDoesNotGate(t *testing.T) {
	s := &Store{}
	stub := &countStub{err: errors.New("db down")}

	// On a lookup error we must not redirect-loop the user into onboarding, and
	// we must not cache anything (the error result is not authoritative).
	if s.needsOnboarding(context.Background(), "user", stub.count) {
		t.Fatal("a lookup error should not gate the user")
	}
	if _, cached := s.hasHousehold.Load("user"); cached {
		t.Fatal("an errored lookup must not populate the cache")
	}
}
