package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/agopalakrishnan/teams360/backend/domain/team"
	"github.com/agopalakrishnan/teams360/backend/infrastructure/persistence/postgres/sqlcgen"
)

// TeamRepository implements the team.Repository interface
type TeamRepository struct {
	db      *sql.DB
	queries *sqlcgen.Queries
}

// NewTeamRepository creates a new repository instance
func NewTeamRepository(db *sql.DB) team.Repository {
	return &TeamRepository{db: db, queries: sqlcgen.New(db)}
}

// FindByID retrieves a team by ID
func (r *TeamRepository) FindByID(ctx context.Context, id string) (*team.Team, error) {
	row, err := r.queries.FindTeamByID(ctx, id)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("team not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find team: %w", err)
	}

	t := buildTeamFromRow(row.ID, row.Name, row.TeamLeadID, row.Cadence, row.DistributionListEmail, row.TeamLeadName, row.CreatedAt, row.UpdatedAt)

	if err := r.hydrateTeam(ctx, t); err != nil {
		return nil, err
	}

	return t, nil
}

// FindAll retrieves all teams
func (r *TeamRepository) FindAll(ctx context.Context) ([]*team.Team, error) {
	rows, err := r.queries.FindAllTeams(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query teams: %w", err)
	}

	teams := make([]*team.Team, 0, len(rows))
	for _, row := range rows {
		t := buildTeamFromRow(row.ID, row.Name, row.TeamLeadID, row.Cadence, row.DistributionListEmail, row.TeamLeadName, row.CreatedAt, row.UpdatedAt)
		if err := r.hydrateTeam(ctx, t); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}

	return teams, nil
}

// FindAllWithDetails retrieves all teams with enriched details
func (r *TeamRepository) FindAllWithDetails(ctx context.Context) ([]team.Team, error) {
	teams, err := r.FindAll(ctx)
	if err != nil {
		return nil, err
	}

	// Convert []*Team to []Team
	result := make([]team.Team, len(teams))
	for i, t := range teams {
		result[i] = *t
	}

	return result, nil
}

// FindByLeadID retrieves all teams led by a specific user
func (r *TeamRepository) FindByLeadID(ctx context.Context, leadID string) ([]*team.Team, error) {
	rows, err := r.queries.FindTeamsByLeadID(ctx, sql.NullString{String: leadID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("failed to query teams by lead: %w", err)
	}

	teams := make([]*team.Team, 0, len(rows))
	for _, row := range rows {
		t := buildTeamFromRow(row.ID, row.Name, row.TeamLeadID, row.Cadence, row.DistributionListEmail, row.TeamLeadName, row.CreatedAt, row.UpdatedAt)
		if err := r.hydrateTeam(ctx, t); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}

	return teams, nil
}

// FindBySupervisorID retrieves all teams where a user is in the supervisor chain
func (r *TeamRepository) FindBySupervisorID(ctx context.Context, supervisorID string) ([]*team.Team, error) {
	rows, err := r.queries.FindTeamsBySupervisorID(ctx, supervisorID)
	if err != nil {
		return nil, fmt.Errorf("failed to query teams by supervisor: %w", err)
	}

	teams := make([]*team.Team, 0, len(rows))
	for _, row := range rows {
		t := buildTeamFromRow(row.ID, row.Name, row.TeamLeadID, row.Cadence, row.DistributionListEmail, row.TeamLeadName, row.CreatedAt, row.UpdatedAt)
		if err := r.hydrateTeam(ctx, t); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}

	return teams, nil
}

// FindMembers retrieves team members as domain Members
func (r *TeamRepository) FindMembers(ctx context.Context, teamID string) ([]*team.Member, error) {
	userIDs, err := r.queries.FindTeamMemberUserIDs(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to query team members: %w", err)
	}

	members := make([]*team.Member, 0, len(userIDs))
	for _, userID := range userIDs {
		members = append(members, &team.Member{UserID: userID, Role: "member"})
	}

	return members, nil
}

// FindTeamMembers retrieves team members with full user details
func (r *TeamRepository) FindTeamMembers(ctx context.Context, teamID string) ([]team.TeamMember, error) {
	rows, err := r.queries.FindTeamMembersDetailed(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to query team members: %w", err)
	}

	members := make([]team.TeamMember, 0, len(rows))
	for _, row := range rows {
		members = append(members, team.TeamMember{
			ID:       row.ID,
			Username: row.Username,
			FullName: row.FullName,
			Email:    row.Email,
		})
	}

	return members, nil
}

// CountTeamMembers returns the number of members in a team
func (r *TeamRepository) CountTeamMembers(ctx context.Context, teamID string) (int, error) {
	count, err := r.queries.CountTeamMembers(ctx, teamID)
	if err != nil {
		return 0, fmt.Errorf("failed to count team members: %w", err)
	}

	return int(count), nil
}

// FindSupervisorChain retrieves the ordered supervisor chain for a team
func (r *TeamRepository) FindSupervisorChain(ctx context.Context, teamID string) ([]*team.SupervisorLink, error) {
	rows, err := r.queries.FindTeamSupervisorChain(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to query supervisor chain: %w", err)
	}

	supervisorChain := make([]*team.SupervisorLink, 0, len(rows))
	for _, row := range rows {
		supervisorChain = append(supervisorChain, &team.SupervisorLink{
			UserID:  row.UserID,
			LevelID: row.HierarchyLevelID,
		})
	}

	return supervisorChain, nil
}

// Save persists a new team
func (r *TeamRepository) Save(ctx context.Context, t *team.Team) error {
	// Begin transaction
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	qtx := r.queries.WithTx(tx)

	// Set timestamps
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now

	// Insert team
	err = qtx.InsertTeam(ctx, sqlcgen.InsertTeamParams{
		ID:                    t.ID,
		Name:                  t.Name,
		TeamLeadID:            nullStringFromPtr(t.TeamLeadID),
		Cadence:               cadenceNullString(t.Cadence),
		DistributionListEmail: nullStringFromPtr(t.DistributionListEmail),
		CreatedAt:             sql.NullTime{Time: t.CreatedAt, Valid: true},
		UpdatedAt:             sql.NullTime{Time: t.UpdatedAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("failed to save team: %w", err)
	}

	// Insert team members
	for _, member := range t.Members {
		if err := qtx.InsertTeamMember(ctx, sqlcgen.InsertTeamMemberParams{TeamID: t.ID, UserID: member.ID}); err != nil {
			return fmt.Errorf("failed to add team member: %w", err)
		}
	}

	// Insert supervisor chain
	chainPtrs := make([]*team.SupervisorLink, len(t.SupervisorChain))
	for i := range t.SupervisorChain {
		chainPtrs[i] = &t.SupervisorChain[i]
	}
	if err := updateSupervisorChainTx(ctx, qtx, t.ID, chainPtrs); err != nil {
		return err
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Update updates an existing team
func (r *TeamRepository) Update(ctx context.Context, t *team.Team) error {
	// Begin transaction
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	qtx := r.queries.WithTx(tx)

	// Update timestamp
	t.UpdatedAt = time.Now()

	// Update team
	rowsAffected, err := qtx.UpdateTeam(ctx, sqlcgen.UpdateTeamParams{
		Name:                  t.Name,
		TeamLeadID:            nullStringFromPtr(t.TeamLeadID),
		Cadence:               cadenceNullString(t.Cadence),
		DistributionListEmail: nullStringFromPtr(t.DistributionListEmail),
		UpdatedAt:             sql.NullTime{Time: t.UpdatedAt, Valid: true},
		ID:                    t.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to update team: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("team not found: %s", t.ID)
	}

	// Update team members: delete existing, insert new
	if err := qtx.DeleteTeamMembersByTeamID(ctx, t.ID); err != nil {
		return fmt.Errorf("failed to delete team members: %w", err)
	}
	for _, member := range t.Members {
		if err := qtx.InsertTeamMember(ctx, sqlcgen.InsertTeamMemberParams{TeamID: t.ID, UserID: member.ID}); err != nil {
			return fmt.Errorf("failed to add team member: %w", err)
		}
	}

	// Update supervisor chain
	chainPtrs := make([]*team.SupervisorLink, len(t.SupervisorChain))
	for i := range t.SupervisorChain {
		chainPtrs[i] = &t.SupervisorChain[i]
	}
	if err := updateSupervisorChainTx(ctx, qtx, t.ID, chainPtrs); err != nil {
		return err
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Delete removes a team
func (r *TeamRepository) Delete(ctx context.Context, id string) error {
	rowsAffected, err := r.queries.DeleteTeam(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to delete team: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("team not found: %s", id)
	}

	return nil
}

// AddMember adds a member to a team
func (r *TeamRepository) AddMember(ctx context.Context, teamID, userID string) error {
	if err := r.queries.AddTeamMember(ctx, sqlcgen.AddTeamMemberParams{TeamID: teamID, UserID: userID}); err != nil {
		return fmt.Errorf("failed to add team member: %w", err)
	}

	return nil
}

// RemoveMember removes a member from a team
func (r *TeamRepository) RemoveMember(ctx context.Context, teamID, userID string) error {
	rowsAffected, err := r.queries.RemoveTeamMember(ctx, sqlcgen.RemoveTeamMemberParams{TeamID: teamID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to remove team member: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("team member not found: %s in team %s", userID, teamID)
	}

	return nil
}

// UpdateSupervisorChain updates the supervisor chain for a team
func (r *TeamRepository) UpdateSupervisorChain(ctx context.Context, teamID string, chain []*team.SupervisorLink) error {
	// Begin transaction
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	qtx := r.queries.WithTx(tx)

	if err := updateSupervisorChainTx(ctx, qtx, teamID, chain); err != nil {
		return err
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Helper functions

// buildTeamFromRow constructs a *team.Team from the common columns shared by
// every FindTeam* query (FindTeamByID, FindAllTeams, FindTeamsByLeadID,
// FindTeamsBySupervisorID) - sqlc generates a distinct row struct per query
// even though these four share the same SELECT list.
func buildTeamFromRow(id, name string, teamLeadID, cadence, distributionListEmail, teamLeadName sql.NullString, createdAt, updatedAt sql.NullTime) *team.Team {
	t := &team.Team{ID: id, Name: name}

	if teamLeadID.Valid {
		t.TeamLeadID = &teamLeadID.String
	}
	if teamLeadName.Valid {
		t.TeamLeadName = &teamLeadName.String
	}
	if cadence.Valid {
		t.Cadence = cadence.String
	} else {
		t.Cadence = "monthly" // default
	}
	if distributionListEmail.Valid {
		t.DistributionListEmail = &distributionListEmail.String
	}
	if createdAt.Valid {
		t.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		t.UpdatedAt = updatedAt.Time
	}

	return t
}

// hydrateTeam populates members, supervisor chain, and tags on an already
// base-populated *team.Team - mirrors the enrichment steps the original
// FindByID/scanTeams performed after the base row scan.
func (r *TeamRepository) hydrateTeam(ctx context.Context, t *team.Team) error {
	members, err := r.FindTeamMembers(ctx, t.ID)
	if err != nil {
		return err
	}
	t.Members = members
	t.MemberCount = len(members)

	supervisorChain, err := r.FindSupervisorChain(ctx, t.ID)
	if err != nil {
		return err
	}
	t.SupervisorChain = make([]team.SupervisorLink, len(supervisorChain))
	for i, link := range supervisorChain {
		t.SupervisorChain[i] = *link
	}

	tags, err := r.fetchTags(ctx, t.ID)
	if err != nil {
		return err
	}
	t.Tags = tags

	return nil
}

// fetchTags is a helper function to get team tags
func (r *TeamRepository) fetchTags(ctx context.Context, teamID string) ([]string, error) {
	// For now, return empty slice since there's no team_tags table in the schema
	// This can be implemented later if needed
	return []string{}, nil
}

// updateSupervisorChainTx updates supervisor chain within a transaction
func updateSupervisorChainTx(ctx context.Context, qtx *sqlcgen.Queries, teamID string, chain []*team.SupervisorLink) error {
	// Delete existing supervisors
	if err := qtx.DeleteTeamSupervisorsByTeamID(ctx, teamID); err != nil {
		return fmt.Errorf("failed to delete team supervisors: %w", err)
	}

	// Insert new supervisors (positions are 1-based per DB constraint)
	for i, supervisor := range chain {
		err := qtx.InsertTeamSupervisor(ctx, sqlcgen.InsertTeamSupervisorParams{
			TeamID:           teamID,
			UserID:           supervisor.UserID,
			HierarchyLevelID: supervisor.LevelID,
			Position:         int32(i + 1),
		})
		if err != nil {
			return fmt.Errorf("failed to save team supervisor: %w", err)
		}
	}

	return nil
}

// nullStringFromPtr converts an optional string field to sql.NullString,
// matching the original behavior of treating nil or "" as NULL.
func nullStringFromPtr(s *string) sql.NullString {
	if s != nil && *s != "" {
		return sql.NullString{String: *s, Valid: true}
	}
	return sql.NullString{}
}

// cadenceNullString converts a cadence value to sql.NullString, defaulting
// to "monthly" when empty - matching the original Save/Update behavior.
func cadenceNullString(cadence string) sql.NullString {
	if cadence != "" {
		return sql.NullString{String: cadence, Valid: true}
	}
	return sql.NullString{String: "monthly", Valid: true}
}
