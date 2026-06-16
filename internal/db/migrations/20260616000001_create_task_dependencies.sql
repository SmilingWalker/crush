-- +goose Up
-- +goose StatementBegin

-- M4-11: task dependency graph. Composite PK (task_id, depends_on_task_id).
-- Both FKs reference team_tasks(id) with CASCADE delete so removing a task
-- cleans up all dependency edges where it appears as either endpoint.
CREATE TABLE IF NOT EXISTS team_task_dependencies (
    task_id           TEXT NOT NULL REFERENCES team_tasks(id) ON DELETE CASCADE,
    depends_on_task_id TEXT NOT NULL REFERENCES team_tasks(id) ON DELETE CASCADE,
    team_id           TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    created_at        INTEGER NOT NULL,  -- epoch millis
    PRIMARY KEY (task_id, depends_on_task_id)
);

-- "what tasks does X depend on?" (blockedBy)
CREATE INDEX IF NOT EXISTS idx_task_deps_task ON team_task_dependencies(task_id);

-- "what tasks depend on X?" (blocks / cascade wake)
CREATE INDEX IF NOT EXISTS idx_task_deps_depends_on ON team_task_dependencies(depends_on_task_id);

-- all dependencies in a team (for graph-load during cycle detection)
CREATE INDEX IF NOT EXISTS idx_task_deps_team ON team_task_dependencies(team_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS team_task_dependencies;
-- +goose StatementEnd
