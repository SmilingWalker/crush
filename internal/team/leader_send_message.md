Send a message to team members.

Parameters:
- team_id: target team (required)
- from_member_id: sender member ID — use the leader_member_id from team_create (required)
- recipient_type: \"direct\", \"broadcast\", or \"role\" (required)
- to_member_id: target member ID (required for direct)
- to_role: target role (for role-based delivery)
- kind: \"message\", \"task_status\", \"task_assignment\", \"shutdown_request\", or \"shutdown_ack\"
- summary: short summary of the message
- payload: message body content

Members receive the message in their mailbox and are woken up to process it.
