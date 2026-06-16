Create a new agent team and a leader member. The leader member ID (returned as leader_member_id) is used as from_member_id when sending messages with team_send_message.

Parameters:
- name: team name (required)
- description: optional description

Returns: team_id, leader_member_id, status. Use team_spawn_member to add worker members.
