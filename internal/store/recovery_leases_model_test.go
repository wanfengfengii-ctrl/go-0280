package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"silage/internal/domain"
	"silage/internal/evidence"
	"silage/internal/sampling"
	"silage/internal/store"
	"silage/internal/task"
)

func TestModel_RecoveryPreservesTaskResourceExclusion(t *testing.T) {
	tests := []struct {
		name         string
		taskExists   bool
		plugged      bool
		callStatus   evidence.CallStatus
		leaseTypes   []sampling.ResourceType
		wantLeases   int
		wantReleased int
		wantConflict bool
	}{
		{
			name:         "unplugged hole keeps drill zone and hole exclusive",
			taskExists:   true,
			leaseTypes:   []sampling.ResourceType{sampling.ResourceDrill, sampling.ResourceZone, sampling.ResourceHole},
			wantLeases:   3,
			wantConflict: true,
		},
		{
			name:         "pending instrument call keeps plugged hole resources exclusive",
			taskExists:   true,
			plugged:      true,
			callStatus:   evidence.CallPending,
			leaseTypes:   []sampling.ResourceType{sampling.ResourceDrill, sampling.ResourceZone, sampling.ResourceHole},
			wantLeases:   3,
			wantConflict: true,
		},
		{
			name:         "retry instrument call keeps plugged hole resources exclusive",
			taskExists:   true,
			plugged:      true,
			callStatus:   evidence.CallRetry,
			leaseTypes:   []sampling.ResourceType{sampling.ResourceDrill, sampling.ResourceZone, sampling.ResourceHole},
			wantLeases:   3,
			wantConflict: true,
		},
		{
			name:         "plugged hole without outstanding call releases task resources",
			taskExists:   true,
			plugged:      true,
			callStatus:   evidence.CallAccepted,
			leaseTypes:   []sampling.ResourceType{sampling.ResourceDrill, sampling.ResourceZone, sampling.ResourceHole},
			wantReleased: 3,
		},
		{
			name:         "orphaned expired resource remains releasable",
			leaseTypes:   []sampling.ResourceType{sampling.ResourceDrill},
			wantReleased: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "recovery.db")
			st, err := store.Open(path, domain.NewWallClock())
			if err != nil {
				t.Fatalf("open initial store: %v", err)
			}

			tx, err := st.Begin(ctx)
			if err != nil {
				t.Fatalf("begin initial transaction: %v", err)
			}
			if tt.taskExists {
				if err := tx.SaveTask(ctx, task.InspectionTask{
					ID: "task-1", SiloID: "silo-1", Status: task.StatusCoring, Generation: 1,
				}); err != nil {
					t.Fatalf("save task: %v", err)
				}
				if err := tx.SaveCell(ctx, "task-1", sampling.SamplingCell{
					Coordinate: domain.Coordinate{Zone: "zone-1", Layer: 1, Depth: 0},
					HoleID:     "hole-1", Covered: true, Plugged: tt.plugged, Generation: 1,
				}); err != nil {
					t.Fatalf("save cell: %v", err)
				}
				if tt.callStatus != "" {
					if err := tx.SaveCall(ctx, evidence.InstrumentCall{
						ID: "call-1", TaskID: "task-1", HoleID: "hole-1", Status: tt.callStatus,
					}); err != nil {
						t.Fatalf("save instrument call: %v", err)
					}
				}
			}
			for _, resourceType := range tt.leaseTypes {
				resourceID := map[sampling.ResourceType]string{
					sampling.ResourceDrill: "drill-1",
					sampling.ResourceZone:  "zone-1",
					sampling.ResourceHole:  "hole-1",
				}[resourceType]
				holeID := ""
				if resourceType == sampling.ResourceHole {
					holeID = "hole-1"
				}
				if err := tx.SaveLease(ctx, sampling.ResourceLease{
					ResourceType: resourceType,
					ResourceID:   resourceID,
					TaskID:       "task-1",
					HoleID:       holeID,
					Token:        "task-1:" + string(resourceType),
					AcquiredAt:   1,
					ExpiresAt:    10,
				}); err != nil {
					t.Fatalf("save %s lease: %v", resourceType, err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit initial state: %v", err)
			}
			if err := st.Close(); err != nil {
				t.Fatalf("close initial store: %v", err)
			}

			recovered, err := store.Open(path, domain.NewWallClock())
			if err != nil {
				t.Fatalf("reopen store: %v", err)
			}
			defer recovered.Close()
			report, err := recovered.Recover(ctx, 100)
			if err != nil {
				t.Fatalf("recover: %v", err)
			}
			if report.ReleasedLeases != tt.wantReleased {
				t.Errorf("released leases = %d, want %d", report.ReleasedLeases, tt.wantReleased)
			}

			check, err := recovered.Begin(ctx)
			if err != nil {
				t.Fatalf("begin verification transaction: %v", err)
			}
			defer check.Rollback()
			leases, err := check.ListLeases(ctx, "task-1")
			if err != nil {
				t.Fatalf("list recovered leases: %v", err)
			}
			if len(leases) != tt.wantLeases {
				t.Errorf("recovered task leases = %d, want %d", len(leases), tt.wantLeases)
			}

			contender := store.NewLeaseCoordinator(check, "task-2", domain.NewWallClock())
			for _, resourceType := range tt.leaseTypes {
				resourceID := map[sampling.ResourceType]string{
					sampling.ResourceDrill: "drill-1",
					sampling.ResourceZone:  "zone-1",
					sampling.ResourceHole:  "hole-1",
				}[resourceType]
				_, err := contender.Acquire(resourceType, resourceID, 100)
				if tt.wantConflict {
					var conflict *domain.Error
					if !errors.As(err, &conflict) || conflict.Code != domain.CodeLeaseConflict {
						t.Errorf("contender acquisition of %s returned %v, want %s", resourceType, err, domain.CodeLeaseConflict)
					}
				} else if err != nil {
					t.Errorf("contender acquisition of released %s: %v", resourceType, err)
				}
			}
		})
	}
}
