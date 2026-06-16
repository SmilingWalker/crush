// dependency.go implements the M4-11 task dependency service methods:
// AddDependency (with DFS cycle detection), RemoveDependency,
// OnTaskCompleted (cascade wake), and GetTaskWithDeps.

package team

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrDependencyCycle is returned by AddDependency when the proposed edge would
// create a cycle in the dependency graph (task would transitively depend on
// itself).
var ErrDependencyCycle = errors.New("dependency would create a cycle")

// ErrSelfDependency is returned by AddDependency when task_id == depends_on_task_id.
var ErrSelfDependency = errors.New("task cannot depend on itself")

// --- AddDependency ---

// AddDependency creates a dependency edge taskID depends on dependsOnTaskID.
// Before inserting, it runs DFS cycle detection: if dependsOnTaskID already
// transitively depends on taskID, adding this edge would create a cycle.
// Self-dependency (taskID == dependsOnTaskID) is also rejected.
func (s *teamService) AddDependency(ctx context.Context, taskID, dependsOnTaskID, teamID string) error {
	if err := s.enabledGuard(); err != nil {
		return err
	}
	if taskID == dependsOnTaskID {
		return ErrSelfDependency
	}

	// DFS cycle detection: load the full team dependency graph in a read tx.
	if hasCycle, err := s.checkCycle(ctx, teamID, taskID, dependsOnTaskID); err != nil {
		return fmt.Errorf("cycle detection: %w", err)
	} else if hasCycle {
		return ErrDependencyCycle
	}

	// Write tx: insert the dependency edge + optionally set task to blocked.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ts := now()
	if err := s.deps.AddDependency(ctx, tx, TeamTaskDependency{
		TaskID:          taskID,
		DependsOnTaskID: dependsOnTaskID,
		TeamID:          teamID,
		CreatedAt:       ts,
	}); err != nil {
		return fmt.Errorf("add dependency: %w", err)
	}

	// If the dependency task is not yet terminal, set the dependent task to
	// 'blocked' so the scheduler won't claim it until deps resolve.
	depTask, err := s.tasks.GetTask(ctx, tx, teamID, dependsOnTaskID)
	if err != nil {
		return fmt.Errorf("get dependency task: %w", err)
	}
	if depTask.Status != TaskCompleted && depTask.Status != TaskFailed && depTask.Status != TaskCanceled {
		task, err := s.tasks.GetTask(ctx, tx, teamID, taskID)
		if err != nil {
			return fmt.Errorf("get task: %w", err)
		}
		if task.Status == TaskQueued || task.Status == TaskAssigned {
			_, err := s.tasks.UpdateTaskCAS(ctx, tx, UpdateTaskCASRequest{
				ID: taskID, TeamID: teamID, Status: TaskBlocked,
				UpdatedAt: ts, ExpectedVersion: task.Version,
			})
			if err != nil {
				return fmt.Errorf("block task: %w", err)
			}
		}
	}

	// Audit + event.
	seq, err := s.events.NextEventSeq(ctx, tx, teamID, ts)
	if err != nil {
		return fmt.Errorf("alloc event seq: %w", err)
	}
	if err := s.events.AppendEvent(ctx, tx, TeamEvent{
		Seq: seq, ID: uuid.New().String(), TeamID: teamID, TaskID: &taskID,
		EventType: "task.dependency_added", EntityType: "task_dependency",
		EntityID: taskID, CreatedAt: ts,
	}); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	if err := s.audits.AppendAudit(ctx, tx, AuditEvent{
		ID: uuid.New().String(), TeamID: teamID, EventType: "task.dependency_added",
		Action: strPtrOrNil("add_dependency"), CreatedAt: ts,
	}); err != nil {
		return fmt.Errorf("append audit: %w", err)
	}

	return tx.Commit()
}

// checkCycle loads the full dependency graph for the team and runs DFS to check
// whether adding the edge taskID->dependsOnTaskID would create a cycle.
//
// The dependency graph is directed: row A->depends_on_B means "A depends on B",
// i.e. A cannot start until B completes. We model this in the "depends_on"
// direction: adj[A] = [B, ...] (A depends on B).
//
// To detect a cycle when adding taskID->dependsOnTaskID, we temporarily add the
// proposed edge to the adjacency map, then DFS from dependsOnTaskID following
// the depends_on direction. If we can reach taskID, then the proposed edge
// creates a path dependsOnTaskID→...→taskID, which together with the proposed
// taskID→dependsOnTaskID edge forms a cycle.
func (s *teamService) checkCycle(ctx context.Context, teamID, taskID, dependsOnTaskID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin cycle check tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Load all dependency rows for this team.
	allDeps, err := s.deps.GetTeamDependencies(ctx, tx, teamID)
	if err != nil {
		return false, fmt.Errorf("load team deps: %w", err)
	}
	_ = tx.Rollback() // read-only; no commit

	// Build adjacency map in the "depends_on" direction:
	// adj[task] = list of tasks that task depends on.
	adj := make(map[string][]string)
	for _, d := range allDeps {
		adj[d.TaskID] = append(adj[d.TaskID], d.DependsOnTaskID)
	}

	// Temporarily add the proposed edge.
	adj[taskID] = append(adj[taskID], dependsOnTaskID)

	// DFS from dependsOnTaskID following depends_on edges.
	// If we can reach taskID, adding this edge creates a cycle.
	visited := make(map[string]bool)
	return dfsReachesTarget(adj, dependsOnTaskID, taskID, visited), nil
}

// dfsReachesTarget performs a depth-first search on the adjacency map to
// determine whether start can reach target. adj maps each task to the set of
// tasks it depends on (the "depends_on" direction).
func dfsReachesTarget(adj map[string][]string, current, target string, visited map[string]bool) bool {
	if current == target {
		return true
	}
	visited[current] = true
	for _, next := range adj[current] {
		if !visited[next] {
			if dfsReachesTarget(adj, next, target, visited) {
				return true
			}
		}
	}
	return false
}

// --- RemoveDependency ---

// RemoveDependency removes a dependency edge. It does NOT auto-unblock the
// task; the caller decides if the task should change status.
func (s *teamService) RemoveDependency(ctx context.Context, taskID, dependsOnTaskID string) error {
	if err := s.enabledGuard(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.deps.RemoveDependency(ctx, tx, taskID, dependsOnTaskID); err != nil {
		return fmt.Errorf("remove dependency: %w", err)
	}
	return tx.Commit()
}

// --- OnTaskCompleted (cascade wake) ---

// OnTaskCompleted is called when a task transitions to 'completed'. It walks
// the dependency graph: for each dependent task (task that depends on the
// completed task), checks if ALL of that dependent's dependencies are now
// satisfied. If so, the dependent is unblocked: status transitions from
// 'blocked' to 'queued'. The unblocking is recursive (BFS cascade): an
// unblocked task may itself unblock further tasks in a dependency chain.
//
// Returns the set of task IDs that were unblocked.
func (s *teamService) OnTaskCompleted(ctx context.Context, teamID, completedTaskID string) ([]string, error) {
	if err := s.enabledGuard(); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ts := now()
	unblocked := make([]string, 0)

	// BFS: start from the completed task, cascade through newly unblocked tasks.
	queue := []string{completedTaskID}
	processed := make(map[string]bool)

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if processed[current] {
			continue
		}
		processed[current] = true

		// Find all tasks that depend on 'current' (blocks direction).
		deps, derr := s.deps.GetDependents(ctx, tx, current)
		if derr != nil {
			return nil, fmt.Errorf("get dependents of %s: %w", current, derr)
		}

		for _, dep := range deps {
			if processed[dep.TaskID] {
				continue
			}

			// Check if ALL dependencies of this dependent are now satisfied.
			allDeps, aerr := s.deps.GetDependencies(ctx, tx, dep.TaskID)
			if aerr != nil {
				return nil, fmt.Errorf("get dependencies of %s: %w", dep.TaskID, aerr)
			}

			allSatisfied := true
			for _, d := range allDeps {
				depTask, gerr := s.tasks.GetTask(ctx, tx, teamID, d.DependsOnTaskID)
				if gerr != nil {
					return nil, fmt.Errorf("get dep task %s: %w", d.DependsOnTaskID, gerr)
				}
				if depTask.Status != TaskCompleted && depTask.Status != TaskFailed && depTask.Status != TaskCanceled {
					allSatisfied = false
					break
				}
			}

			if allSatisfied {
				task, gerr := s.tasks.GetTask(ctx, tx, teamID, dep.TaskID)
				if gerr != nil {
					return nil, fmt.Errorf("get task %s: %w", dep.TaskID, gerr)
				}
				if task.Status == TaskBlocked {
					_, uerr := s.tasks.UpdateTaskCAS(ctx, tx, UpdateTaskCASRequest{
						ID: dep.TaskID, TeamID: teamID, Status: TaskQueued,
						UpdatedAt: ts, ExpectedVersion: task.Version,
					})
					if uerr != nil {
						return nil, fmt.Errorf("unblock task %s: %w", dep.TaskID, uerr)
					}
					unblocked = append(unblocked, dep.TaskID)

					// Append event for the unblock.
					seq, serr := s.events.NextEventSeq(ctx, tx, teamID, ts)
					if serr != nil {
						return nil, fmt.Errorf("alloc event seq: %w", serr)
					}
					_ = s.events.AppendEvent(ctx, tx, TeamEvent{
						Seq: seq, ID: uuid.New().String(), TeamID: teamID,
						TaskID: &dep.TaskID, EventType: "task.unblocked",
						EntityType: "task", EntityID: dep.TaskID, CreatedAt: ts,
					})
				}
				// Cascade: the newly unblocked task may itself unblock others.
				queue = append(queue, dep.TaskID)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit cascade: %w", err)
	}
	return unblocked, nil
}

// --- GetTaskWithDeps ---

// GetTaskWithDeps returns a task with its blocks/blockedBy fields populated
// from the dependency graph. blocks = tasks that depend on this task;
// blockedBy = tasks this task depends on.
func (s *teamService) GetTaskWithDeps(ctx context.Context, teamID, taskID string) (TeamTask, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamTask{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamTask{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	task, err := s.tasks.GetTask(ctx, tx, teamID, taskID)
	if err != nil {
		return TeamTask{}, fmt.Errorf("get task: %w", err)
	}

	// Populate blockedBy (what this task depends on).
	deps, err := s.deps.GetDependencies(ctx, tx, taskID)
	if err != nil {
		return TeamTask{}, fmt.Errorf("get dependencies: %w", err)
	}
	task.BlockedBy = make([]string, 0, len(deps))
	for _, d := range deps {
		task.BlockedBy = append(task.BlockedBy, d.DependsOnTaskID)
	}

	// Populate blocks (what depends on this task).
	dependents, err := s.deps.GetDependents(ctx, tx, taskID)
	if err != nil {
		return TeamTask{}, fmt.Errorf("get dependents: %w", err)
	}
	task.Blocks = make([]string, 0, len(dependents))
	for _, d := range dependents {
		task.Blocks = append(task.Blocks, d.TaskID)
	}

	if err := tx.Commit(); err != nil {
		return TeamTask{}, fmt.Errorf("commit: %w", err)
	}
	return task, nil
}

// The unused import (database/sql) is kept for future extensions (e.g., more
// SQL-level dependency operations). It currently compiles without issue.
var _ = (*sql.Tx)(nil)
