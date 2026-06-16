Spawn a new AI member in an existing team. The member gets its own agent runner, enters the team's runtime loop, and begins listening for tasks and messages.

Parameters:
- team_id: target team (required)
- name: display name for the member (required)
- role: e.g. "programmer", "reviewer", "tester" (required)
- model_type: "inherit" (default), "large", "small" (optional)
- permission_mode: "default" (ask user), "auto" (optional)

The member starts in "idle" state, ready to receive tasks.
