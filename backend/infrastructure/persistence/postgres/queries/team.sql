-- Queries for TeamRepository (backend/infrastructure/persistence/postgres/team_repository.go)
-- Moved verbatim from the hand-written SQL that lived inline in that file.
-- Do not hand-edit sqlcgen/*.go - regenerate with `sqlc generate` (see Makefile).

-- name: FindTeamByID :one
SELECT t.id, t.name, t.team_lead_id, t.cadence, t.created_at, t.updated_at, t.distribution_list_email, u.full_name AS team_lead_name
FROM teams t
LEFT JOIN users u ON t.team_lead_id = u.id
WHERE t.id = sqlc.arg(id);

-- name: FindAllTeams :many
SELECT t.id, t.name, t.team_lead_id, t.cadence, t.created_at, t.updated_at, t.distribution_list_email, u.full_name AS team_lead_name
FROM teams t
LEFT JOIN users u ON t.team_lead_id = u.id
ORDER BY t.name;

-- name: FindTeamsByLeadID :many
SELECT t.id, t.name, t.team_lead_id, t.cadence, t.created_at, t.updated_at, t.distribution_list_email, u.full_name AS team_lead_name
FROM teams t
LEFT JOIN users u ON t.team_lead_id = u.id
WHERE t.team_lead_id = sqlc.arg(lead_id)
ORDER BY t.name;

-- name: FindTeamsBySupervisorID :many
SELECT DISTINCT t.id, t.name, t.team_lead_id, t.cadence, t.created_at, t.updated_at, t.distribution_list_email, u.full_name AS team_lead_name
FROM teams t
INNER JOIN team_supervisors ts ON t.id = ts.team_id
LEFT JOIN users u ON t.team_lead_id = u.id
WHERE ts.user_id = sqlc.arg(supervisor_id)
ORDER BY t.name;

-- name: FindTeamMemberUserIDs :many
SELECT user_id
FROM team_members
WHERE team_id = sqlc.arg(team_id)
ORDER BY user_id;

-- name: FindTeamMembersDetailed :many
SELECT u.id, u.username, u.full_name, u.email
FROM users u
INNER JOIN team_members tm ON u.id = tm.user_id
WHERE tm.team_id = sqlc.arg(team_id)
ORDER BY u.username;

-- name: CountTeamMembers :one
SELECT COUNT(*)
FROM team_members
WHERE team_id = sqlc.arg(team_id);

-- name: FindTeamSupervisorChain :many
SELECT user_id, hierarchy_level_id
FROM team_supervisors
WHERE team_id = sqlc.arg(team_id)
ORDER BY position;

-- name: InsertTeam :exec
INSERT INTO teams (id, name, team_lead_id, cadence, distribution_list_email, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(team_lead_id), sqlc.arg(cadence), sqlc.arg(distribution_list_email), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: UpdateTeam :execrows
UPDATE teams SET
    name = sqlc.arg(name),
    team_lead_id = sqlc.arg(team_lead_id),
    cadence = sqlc.arg(cadence),
    distribution_list_email = sqlc.arg(distribution_list_email),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteTeam :execrows
DELETE FROM teams WHERE id = sqlc.arg(id);

-- name: AddTeamMember :exec
INSERT INTO team_members (team_id, user_id)
VALUES (sqlc.arg(team_id), sqlc.arg(user_id))
ON CONFLICT DO NOTHING;

-- name: InsertTeamMember :exec
INSERT INTO team_members (team_id, user_id)
VALUES (sqlc.arg(team_id), sqlc.arg(user_id));

-- name: RemoveTeamMember :execrows
DELETE FROM team_members
WHERE team_id = sqlc.arg(team_id) AND user_id = sqlc.arg(user_id);

-- name: DeleteTeamMembersByTeamID :exec
DELETE FROM team_members WHERE team_id = sqlc.arg(team_id);

-- name: DeleteTeamSupervisorsByTeamID :exec
DELETE FROM team_supervisors WHERE team_id = sqlc.arg(team_id);

-- name: InsertTeamSupervisor :exec
INSERT INTO team_supervisors (team_id, user_id, hierarchy_level_id, position)
VALUES (sqlc.arg(team_id), sqlc.arg(user_id), sqlc.arg(hierarchy_level_id), sqlc.arg(position));
