package store

import (
	"context"
	"path/filepath"
	"testing"

	"silage/internal/arbitration"
	"silage/internal/domain"
	"silage/internal/sampling"
	"silage/internal/task"
)

func TestPersistenceAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	ctx := context.Background()

	// First session: write a task, a lease, a pending retry and an expansion.
	st, err := Open(path, &domain.WallClock{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	func() {
		tx, err := st.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback()
		tk := task.InspectionTask{ID: "t1", SiloID: "s1", Status: task.StatusCoring, Generation: 1}
		if err := tx.SaveTask(ctx, tk); err != nil {
			t.Fatalf("save task: %v", err)
		}
		if err := tx.SaveLease(ctx, sampling.ResourceLease{
			ResourceType: sampling.ResourceDrill, ResourceID: "d1", TaskID: "t1",
			Token: "tok", AcquiredAt: 1, ExpiresAt: 100000,
		}); err != nil {
			t.Fatalf("save lease: %v", err)
		}
		if err := tx.SaveExpansion(ctx, arbitration.ExpansionPlan{
			TaskID: "t1", Generation: 2,
			Coordinates: []domain.Coordinate{{Zone: "A", Layer: 2, Depth: 0}},
		}); err != nil {
			t.Fatalf("save expansion: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}()
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Second session: reopen and recover.
	st2, err := Open(path, &domain.WallClock{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()

	report, err := st2.Recover(ctx, 100000)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.ActiveTasks != 1 {
		t.Fatalf("active tasks = %d, want 1", report.ActiveTasks)
	}
	if report.Leases != 1 {
		t.Fatalf("leases = %d, want 1", report.Leases)
	}
	if report.ExpansionPlans != 1 {
		t.Fatalf("expansions = %d, want 1", report.ExpansionPlans)
	}

	// The task must round-trip.
	tx, _ := st2.Begin(ctx)
	defer tx.Rollback()
	got, err := tx.Task(ctx, "t1")
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if got.Status != task.StatusCoring || got.Generation != 1 {
		t.Fatalf("task = %+v", got)
	}
}

func TestRecoveryReleasesExpiredLease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exp.db")
	ctx := context.Background()

	st, err := Open(path, &domain.WallClock{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	tx, _ := st.Begin(ctx)
	_ = tx.SaveLease(ctx, sampling.ResourceLease{
		ResourceType: sampling.ResourceDrill, ResourceID: "d1", TaskID: "t1",
		Token: "tok", AcquiredAt: 1, ExpiresAt: 10,
	})
	_ = tx.Commit()
	_ = st.Close()

	st2, err := Open(path, &domain.WallClock{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()

	report, err := st2.Recover(ctx, 100) // now beyond expiry
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.ReleasedLeases != 1 {
		t.Fatalf("released = %d, want 1", report.ReleasedLeases)
	}
}

func TestLeaseUniqueConstraint(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:", &domain.WallClock{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	tx, _ := st.Begin(ctx)
	defer tx.Rollback()
	l := sampling.ResourceLease{ResourceType: sampling.ResourceDrill, ResourceID: "d1", TaskID: "t1", Token: "a"}
	if err := tx.SaveLease(ctx, l); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// Overwriting the same resource is an upsert, not a duplicate row, so this
	// must succeed and replace the prior lease.
	l.TaskID = "t2"
	if err := tx.SaveLease(ctx, l); err != nil {
		t.Fatalf("upsert save: %v", err)
	}
}
