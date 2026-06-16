package team

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedTeamWithTasks creates a team + a lead member + N tasks and returns their IDs.
func seedTeamWithTasks(t *testing.T, sqlDB *sql.DB, q interface{}, n int) (teamID, leadID string, taskIDs []string) {
	t.Helper()
	teamID = "team-deps"
	leadID = "lead-1"

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		ts := func() int64 { return now().UnixMilli() }
		// Create team
		if _, err := tx.ExecContext(context.Background(),
			`INSERT INTO teams (id, workspace_id, leader_session_id, name, status, version, cost_so_far_micros, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'created', 1, 0, ?, ?)`,
			teamID, "ws", "lead-sess", "dep-team", ts(), ts()); err != nil {
			return err
		}
		// Create lead member
		if _, err := tx.ExecContext(context.Background(),
			`INSERT INTO team_members (id, team_id, name, role, agent_profile, status, last_event_seq, cost_so_far_micros, version, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, 'created', 0, 0, 1, ?, ?)`,
			leadID, teamID, "lead", "leader", "{}", ts(), ts()); err != nil {
			return err
		}
		// Create N tasks
		for i := 0; i < n; i++ {
			taskID := "task-" + string(rune('A'+i))
			taskIDs = append(taskIDs, taskID)
			if _, err := tx.ExecContext(context.Background(),
				`INSERT INTO team_tasks (id, team_id, title, description, status, assignee_member_id, created_by_member_id, priority, version, created_at, updated_at)
				 VALUES (?, ?, ?, NULL, 'queued', NULL, ?, 0, 1, ?, ?)`,
				taskID, teamID, "task "+string(rune('A'+i)), leadID, ts(), ts()); err != nil {
				return err
			}
		}
		return nil
	})
	return
}

func TestDependencyStore_AddAndGet(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teamID, _, taskIDs := seedTeamWithTasks(t, sqlDB, q, 3)
	store := NewDependencyStore(q)

	// Add A -> B (A depends on B)
	dep := TeamTaskDependency{
		TaskID: taskIDs[0], DependsOnTaskID: taskIDs[1], TeamID: teamID, CreatedAt: now(),
	}
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return store.AddDependency(context.Background(), tx, dep)
	})

	// GetDependencies(A) → [B]
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		deps, err := store.GetDependencies(context.Background(), tx, taskIDs[0])
		require.NoError(t, err)
		require.Len(t, deps, 1)
		assert.Equal(t, taskIDs[1], deps[0].DependsOnTaskID)
		return nil
	})

	// GetDependents(B) → [A]
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		dependents, err := store.GetDependents(context.Background(), tx, taskIDs[1])
		require.NoError(t, err)
		require.Len(t, dependents, 1)
		assert.Equal(t, taskIDs[0], dependents[0].TaskID)
		return nil
	})
}

func TestDependencyStore_RemoveDependency(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teamID, _, taskIDs := seedTeamWithTasks(t, sqlDB, q, 2)
	store := NewDependencyStore(q)

	// Add A -> B, then remove it.
	dep := TeamTaskDependency{
		TaskID: taskIDs[0], DependsOnTaskID: taskIDs[1], TeamID: teamID, CreatedAt: now(),
	}
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return store.AddDependency(context.Background(), tx, dep)
	})
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return store.RemoveDependency(context.Background(), tx, taskIDs[0], taskIDs[1])
	})

	// After removal, GetDependencies(A) → empty.
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		deps, err := store.GetDependencies(context.Background(), tx, taskIDs[0])
		require.NoError(t, err)
		assert.Len(t, deps, 0)
		return nil
	})
}

func TestDependencyStore_GetTeamDependencies(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teamID, _, taskIDs := seedTeamWithTasks(t, sqlDB, q, 3)
	store := NewDependencyStore(q)

	// Add A -> B, B -> C, A -> C (three edges).
	ts := now()
	edges := []TeamTaskDependency{
		{TaskID: taskIDs[0], DependsOnTaskID: taskIDs[1], TeamID: teamID, CreatedAt: ts},
		{TaskID: taskIDs[1], DependsOnTaskID: taskIDs[2], TeamID: teamID, CreatedAt: ts},
		{TaskID: taskIDs[0], DependsOnTaskID: taskIDs[2], TeamID: teamID, CreatedAt: ts},
	}
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		for _, e := range edges {
			if err := store.AddDependency(context.Background(), tx, e); err != nil {
				return err
			}
		}
		return nil
	})

	// GetTeamDependencies → all 3 edges.
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		all, err := store.GetTeamDependencies(context.Background(), tx, teamID)
		require.NoError(t, err)
		assert.Len(t, all, 3)
		return nil
	})
}

func TestAddDependency_SelfDependency(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	task, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "x", CreatedByMemberID: m.ID})

	err := svc.AddDependency(ctx, task.ID, task.ID, snap.Team.ID)
	require.ErrorIs(t, err, ErrSelfDependency)
}

func TestAddDependency_CycleDetection_Direct(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})

	// A -> B should succeed.
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))

	// B -> A should fail (cycle: A->B and B->A means A depends on itself transitively).
	err := svc.AddDependency(ctx, taskB.ID, taskA.ID, snap.Team.ID)
	require.ErrorIs(t, err, ErrDependencyCycle)
}

func TestAddDependency_CycleDetection_Transitive(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})
	taskC, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "C", CreatedByMemberID: m.ID})

	// A -> B, B -> C (A depends on B, B depends on C).
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))
	require.NoError(t, svc.AddDependency(ctx, taskB.ID, taskC.ID, snap.Team.ID))

	// C -> A should fail (cycle: C -> A -> B -> C via existing edges).
	err := svc.AddDependency(ctx, taskC.ID, taskA.ID, snap.Team.ID)
	require.ErrorIs(t, err, ErrDependencyCycle)
}

func TestAddDependency_NoFalsePositive(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})
	taskC, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "C", CreatedByMemberID: m.ID})
	taskD, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "D", CreatedByMemberID: m.ID})

	// A -> B, C -> D (two separate chains).
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))
	require.NoError(t, svc.AddDependency(ctx, taskC.ID, taskD.ID, snap.Team.ID))

	// D -> A should succeed (no cycle — different chains).
	require.NoError(t, svc.AddDependency(ctx, taskD.ID, taskA.ID, snap.Team.ID))
}

func TestAddDependency_SetBlocked(t *testing.T) {
	svc, sqlDB := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})

	// A -> B: B is not yet completed, so A should be set to 'blocked'.
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))

	// Verify A is now blocked.
	got, err := svc.GetTask(ctx, snap.Team.ID, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskBlocked, got.Status)

	// Verify migration table row exists.
	var count int
	row := sqlDB.QueryRow(`SELECT count(*) FROM team_task_dependencies WHERE task_id = ? AND depends_on_task_id = ?`, taskA.ID, taskB.ID)
	require.NoError(t, row.Scan(&count))
	assert.Equal(t, 1, count)
}

func TestOnTaskCompleted_CascadeWake_Single(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})

	// A depends on B. After SetBlocked by AddDependency, A is 'blocked'.
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))

	// Complete B → should unblock A.
	_, err := svc.UpdateTask(ctx, UpdateTaskRequest{ID: taskB.ID, TeamID: snap.Team.ID, Status: TaskCompleted})
	require.NoError(t, err)

	// A should now be queued.
	got, err := svc.GetTask(ctx, snap.Team.ID, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskQueued, got.Status)
}

func TestOnTaskCompleted_CascadeWake_Chain(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})
	taskC, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "C", CreatedByMemberID: m.ID})

	// A -> B -> C (A depends on B, B depends on C).
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))
	require.NoError(t, svc.AddDependency(ctx, taskB.ID, taskC.ID, snap.Team.ID))

	// Complete C → unblocks B → B queues → B doesn't auto-complete, but cascade
	// checks B's deps (only C, now completed) → B unblocks to queued.
	// Then cascade checks tasks that depend on B (taskA): A's deps are [B], B is
	// now queued (not completed), so A stays blocked.
	_, err := svc.UpdateTask(ctx, UpdateTaskRequest{ID: taskC.ID, TeamID: snap.Team.ID, Status: TaskCompleted})
	require.NoError(t, err)

	// B should be queued (unblocked).
	b, err := svc.GetTask(ctx, snap.Team.ID, taskB.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskQueued, b.Status, "B should be unblocked after C completes")

	// A should still be blocked (B is queued, not completed).
	a, err := svc.GetTask(ctx, snap.Team.ID, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskBlocked, a.Status, "A stays blocked because B is not yet completed")

	// Now complete B → should cascade to unblock A.
	_, err = svc.UpdateTask(ctx, UpdateTaskRequest{ID: taskB.ID, TeamID: snap.Team.ID, Status: TaskCompleted})
	require.NoError(t, err)

	a, err = svc.GetTask(ctx, snap.Team.ID, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskQueued, a.Status, "A should be unblocked after B completes")
}

func TestOnTaskCompleted_MultipleDependencies(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})
	taskC, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "C", CreatedByMemberID: m.ID})

	// A depends on B AND C (AND gate: both must complete before A unblocks).
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskC.ID, snap.Team.ID))

	// Complete B only → A stays blocked (C not done).
	_, err := svc.UpdateTask(ctx, UpdateTaskRequest{ID: taskB.ID, TeamID: snap.Team.ID, Status: TaskCompleted})
	require.NoError(t, err)

	a, err := svc.GetTask(ctx, snap.Team.ID, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskBlocked, a.Status, "A stays blocked when only one of two deps completed")

	// Complete C → now A unblocks.
	_, err = svc.UpdateTask(ctx, UpdateTaskRequest{ID: taskC.ID, TeamID: snap.Team.ID, Status: TaskCompleted})
	require.NoError(t, err)

	a, err = svc.GetTask(ctx, snap.Team.ID, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskQueued, a.Status, "A unblocked after both deps completed")
}

func TestOnTaskCompleted_DependencyFailed_Unblocks(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})

	// A depends on B.
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))

	// Fail B → A should still unblock (failed deps satisfy the gate).
	_, err := svc.UpdateTask(ctx, UpdateTaskRequest{ID: taskB.ID, TeamID: snap.Team.ID, Status: TaskFailed})
	require.NoError(t, err)

	a, err := svc.GetTask(ctx, snap.Team.ID, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskQueued, a.Status, "A unblocked when dependency fails")
}

func TestGetTaskWithDeps_PopulatesFields(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})
	taskC, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "C", CreatedByMemberID: m.ID})

	// A -> B, A -> C (A depends on B and C).
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskC.ID, snap.Team.ID))

	// GetTaskWithDeps(A) → blockedBy=[B,C], blocks=[].
	got, err := svc.GetTaskWithDeps(ctx, snap.Team.ID, taskA.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{taskB.ID, taskC.ID}, got.BlockedBy)
	assert.Empty(t, got.Blocks)

	// GetTaskWithDeps(B) → blockedBy=[], blocks=[A].
	got, err = svc.GetTaskWithDeps(ctx, snap.Team.ID, taskB.ID)
	require.NoError(t, err)
	assert.Empty(t, got.BlockedBy)
	assert.ElementsMatch(t, []string{taskA.ID}, got.Blocks)
}

func TestRemoveDependency_Service(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})

	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))
	require.NoError(t, svc.RemoveDependency(ctx, taskA.ID, taskB.ID))

	// GetTaskWithDeps(A) → empty.
	got, err := svc.GetTaskWithDeps(ctx, snap.Team.ID, taskA.ID)
	require.NoError(t, err)
	assert.Empty(t, got.BlockedBy)
}

func TestFeatureGate_BlocksDependencyOps(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	svc := NewService(
		sqlDB,
		NewTeamStore(q), NewMemberStore(q), NewTaskStore(q),
		NewRunStore(q), NewEventStore(q), NewAuditStore(q),
		NewMailboxStore(q), NewDependencyStore(q),
		// NO WithEnabledGate → default disabled
	)
	ctx := context.Background()

	require.ErrorIs(t, svc.AddDependency(ctx, "t1", "t2", "team"), ErrFeatureDisabled)
	require.ErrorIs(t, svc.RemoveDependency(ctx, "t1", "t2"), ErrFeatureDisabled)
	_, err := svc.OnTaskCompleted(ctx, "team", "t1")
	require.ErrorIs(t, err, ErrFeatureDisabled)
	_, err = svc.GetTaskWithDeps(ctx, "team", "t1")
	require.ErrorIs(t, err, ErrFeatureDisabled)
}

func TestOnTaskCompleted_ExplicitCall(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "c", Role: "p", AgentProfile: "{}"})
	taskA, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "A", CreatedByMemberID: m.ID})
	taskB, _ := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "B", CreatedByMemberID: m.ID})

	// A depends on B.
	require.NoError(t, svc.AddDependency(ctx, taskA.ID, taskB.ID, snap.Team.ID))

	// Directly complete B (not via UpdateTask) → cascade shouldn't trigger from
	// AddDependency alone. Use OnTaskCompleted directly.
	unblocked, err := svc.OnTaskCompleted(ctx, snap.Team.ID, taskB.ID)
	require.NoError(t, err)
	assert.Len(t, unblocked, 0, "B not yet completed, no cascade")

	// Actually complete B via UpdateTask, then OnTaskCompleted explicitly.
	_, err = svc.UpdateTask(ctx, UpdateTaskRequest{ID: taskB.ID, TeamID: snap.Team.ID, Status: TaskCompleted})
	require.NoError(t, err)
	// UpdateTask already triggered OnTaskCompleted, so A should be queued.
	a, err := svc.GetTask(ctx, snap.Team.ID, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskQueued, a.Status, "A unblocked by UpdateTask cascade")
}
