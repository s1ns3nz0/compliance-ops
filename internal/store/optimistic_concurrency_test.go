package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func importedPartRequirement(t *testing.T, s Store, ctx context.Context) Requirement {
	t.Helper()
	res, err := s.ImportFramework(ctx, partsDoc(t), "importer")
	if err != nil {
		t.Fatal(err)
	}
	reqs, _, err := s.ListRequirements(ctx, RequirementFilter{FrameworkID: res.Framework.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reqs {
		if r.ControlID == "ac-2" {
			return r
		}
	}
	t.Fatal("ac-2 requirement not imported")
	return Requirement{}
}

func TestPartTrackingExpectedUpdatedAtNullCreatesOnlyMissingRow(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		r := importedPartRequirement(t, s, ctx)
		owner := "alice"
		createOnly := UpdatedAtPrecondition{State: UpdatedAtNull}
		created, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt.a", PartPatch{Owner: &owner, ExpectedUpdatedAt: createOnly}, "alice")
		if err != nil {
			t.Fatalf("create with explicit null precondition: %v", err)
		}
		if created.Owner != owner || created.UpdatedAt.IsZero() {
			t.Fatalf("created row = %+v", created)
		}
		owner = "bob"
		if _, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt.a", PartPatch{Owner: &owner, ExpectedUpdatedAt: createOnly}, "bob"); !errors.Is(err, ErrConflict) {
			t.Fatalf("second create with explicit null error = %v, want ErrConflict", err)
		}
		rows, err := s.GetPartTracking(ctx, r.ID)
		if err != nil || len(rows) != 1 || rows[0].Owner != "alice" {
			t.Fatalf("stale create mutated row: rows=%+v err=%v", rows, err)
		}
	})
}

func exerciseConcurrentExplicitNullPartCreation(t *testing.T, s Store, ctx context.Context) {
	t.Helper()
	r := importedPartRequirement(t, s, ctx)
	createOnly := UpdatedAtPrecondition{State: UpdatedAtNull}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, owner := range []string{"writer-a", "writer-b"} {
		owner := owner
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt.a", PartPatch{Owner: &owner, ExpectedUpdatedAt: createOnly}, owner)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var success, preconditionFailed int
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrPreconditionFailed):
			preconditionFailed++
		default:
			t.Fatalf("writer error = %v", err)
		}
	}
	if success != 1 || preconditionFailed != 1 {
		t.Fatalf("success=%d preconditionFailed=%d, want exactly one each", success, preconditionFailed)
	}
	rows, err := s.GetPartTracking(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].PartID != "ac-2_smt.a" || rows[0].UpdatedAt.IsZero() || (rows[0].Owner != "writer-a" && rows[0].Owner != "writer-b") {
		t.Fatalf("final part tracking row = %+v", rows)
	}
}

func TestMemoryPartTrackingConcurrentExplicitNullCreation(t *testing.T) {
	exerciseConcurrentExplicitNullPartCreation(t, NewMemory(), context.Background())
}

func TestPostgresPartTrackingConcurrentExplicitNullCreation(t *testing.T) {
	pool, ctx := scratchDatabase(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	exerciseConcurrentExplicitNullPartCreation(t, NewPostgres(pool), ctx)
}

func TestPartTrackingExpectedUpdatedAtValueAndOmitted(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		r := importedPartRequirement(t, s, ctx)
		owner := "legacy"
		first, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt.a", PartPatch{Owner: &owner}, "legacy")
		if err != nil {
			t.Fatal(err)
		}

		owner = "exact"
		exact := UpdatedAtPrecondition{State: UpdatedAtValue, Value: first.UpdatedAt.Add(499 * time.Nanosecond)}
		second, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt.a", PartPatch{Owner: &owner, ExpectedUpdatedAt: exact}, "exact")
		if err != nil {
			t.Fatalf("exact update: %v", err)
		}
		if second.Owner != owner || !second.UpdatedAt.After(first.UpdatedAt) {
			t.Fatalf("exact update = %+v after %+v", second, first)
		}

		owner = "stale"
		stale := UpdatedAtPrecondition{State: UpdatedAtValue, Value: first.UpdatedAt}
		if _, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt.a", PartPatch{Owner: &owner, ExpectedUpdatedAt: stale}, "stale"); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale update error = %v, want ErrConflict", err)
		}
		owner = "legacy-again"
		if _, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt.a", PartPatch{Owner: &owner}, "legacy"); err != nil {
			t.Fatalf("omitted legacy update: %v", err)
		}
	})
}

func TestRequirementExpectedUpdatedAtAndConcurrentWriters(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		r := importedPartRequirement(t, s, ctx)
		notes := "exact"
		exact := UpdatedAtPrecondition{State: UpdatedAtValue, Value: r.UpdatedAt.Add(499 * time.Nanosecond)}
		updated, err := s.UpdateRequirement(ctx, r.ID, RequirementPatch{Notes: &notes, ExpectedUpdatedAt: exact}, "exact")
		if err != nil {
			t.Fatalf("exact update: %v", err)
		}
		if updated.Notes != notes || !updated.UpdatedAt.After(r.UpdatedAt) {
			t.Fatalf("updated requirement = %+v", updated)
		}

		notes = "stale"
		if _, err := s.UpdateRequirement(ctx, r.ID, RequirementPatch{Notes: &notes, ExpectedUpdatedAt: exact}, "stale"); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale requirement update error = %v", err)
		}
		notes = "legacy"
		legacy, err := s.UpdateRequirement(ctx, r.ID, RequirementPatch{Notes: &notes}, "legacy")
		if err != nil || legacy.Notes != notes {
			t.Fatalf("omitted requirement update = %+v, %v", legacy, err)
		}

		precondition := UpdatedAtPrecondition{State: UpdatedAtValue, Value: legacy.UpdatedAt}
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, value := range []string{"writer-a", "writer-b"} {
			value := value
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := s.UpdateRequirement(ctx, r.ID, RequirementPatch{Notes: &value, ExpectedUpdatedAt: precondition}, value)
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		assertOneSuccessOneConflict(t, errs)
	})
}

func assertOneSuccessOneConflict(t *testing.T, errs <-chan error) {
	t.Helper()
	var success, conflict int
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrConflict):
			conflict++
		default:
			t.Fatalf("writer error = %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d, want exactly one each", success, conflict)
	}
}

func TestScopeCategoryExpectedUpdatedAtAndDelete(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		created, err := s.CreateScopeCategory(ctx, "scope", "creator")
		if err != nil {
			t.Fatal(err)
		}
		exact := UpdatedAtPrecondition{State: UpdatedAtValue, Value: created.UpdatedAt.Add(499 * time.Nanosecond)}
		updated, err := s.UpdateScopeCategory(ctx, created.ID, "renamed", exact, "exact")
		if err != nil || updated.Name != "renamed" || !updated.UpdatedAt.After(created.UpdatedAt) {
			t.Fatalf("exact scope update = %+v, %v", updated, err)
		}
		if _, err := s.UpdateScopeCategory(ctx, created.ID, "wrong", exact, "stale"); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale scope update error = %v", err)
		}
		legacy, err := s.UpdateScopeCategory(ctx, created.ID, "legacy", UpdatedAtPrecondition{}, "legacy")
		if err != nil || legacy.Name != "legacy" {
			t.Fatalf("legacy scope update = %+v, %v", legacy, err)
		}

		precondition := UpdatedAtPrecondition{State: UpdatedAtValue, Value: legacy.UpdatedAt}
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, name := range []string{"writer-a", "writer-b"} {
			name := name
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := s.UpdateScopeCategory(ctx, created.ID, name, precondition, name)
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		assertOneSuccessOneConflict(t, errs)

		current, err := s.ListScopeCategories(ctx, "writer", 10)
		if err != nil || len(current) != 1 {
			t.Fatalf("current scope = %+v, %v", current, err)
		}
		if err := s.DeleteScopeCategory(ctx, created.ID, precondition, "stale-delete"); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale scope delete error = %v", err)
		}
		deleteExact := UpdatedAtPrecondition{State: UpdatedAtValue, Value: current[0].UpdatedAt}
		if err := s.DeleteScopeCategory(ctx, created.ID, deleteExact, "delete"); err != nil {
			t.Fatalf("exact scope delete: %v", err)
		}
	})
}

func TestPartTrackingNeverRegressesRequirementUpdatedAt(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		if m, ok := s.(*Memory); ok {
			fixed := time.Date(2026, 9, 16, 12, 0, 0, 123456789, time.UTC)
			m.Now = func() time.Time { return fixed }
		}
		r := importedPartRequirement(t, s, ctx)
		notes := "newer requirement mutation"
		updated, err := s.UpdateRequirement(ctx, r.ID, RequirementPatch{Notes: &notes}, "notes")
		if err != nil {
			t.Fatal(err)
		}
		owner := "part writer"
		if _, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt.a", PartPatch{Owner: &owner}, "part"); err != nil {
			t.Fatal(err)
		}
		after, err := s.GetRequirement(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !after.UpdatedAt.After(updated.UpdatedAt) {
			t.Fatalf("part update did not advance requirement updatedAt: %v -> %v", updated.UpdatedAt, after.UpdatedAt)
		}
	})
}

func TestPartTrackingConcurrentWritersWithSameExpectedUpdatedAt(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		r := importedPartRequirement(t, s, ctx)
		owner := "initial"
		first, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt.a", PartPatch{Owner: &owner}, "initial")
		if err != nil {
			t.Fatal(err)
		}
		precondition := UpdatedAtPrecondition{State: UpdatedAtValue, Value: first.UpdatedAt}
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, nextOwner := range []string{"writer-a", "writer-b"} {
			nextOwner := nextOwner
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt.a", PartPatch{Owner: &nextOwner, ExpectedUpdatedAt: precondition}, nextOwner)
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		var success, conflict int
		for err := range errs {
			switch {
			case err == nil:
				success++
			case errors.Is(err, ErrConflict):
				conflict++
			default:
				t.Fatalf("writer error = %v", err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatalf("success=%d conflict=%d, want exactly one each", success, conflict)
		}
	})
}
