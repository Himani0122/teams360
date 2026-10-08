package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/agopalakrishnan/teams360/backend/domain/healthcheck"
	"github.com/agopalakrishnan/teams360/backend/infrastructure/persistence/postgres/sqlcgen"
)

// HealthCheckRepository implements the healthcheck.Repository interface
type HealthCheckRepository struct {
	db      *sql.DB
	queries *sqlcgen.Queries
}

// NewHealthCheckRepository creates a new repository instance
func NewHealthCheckRepository(db *sql.DB) healthcheck.Repository {
	return &HealthCheckRepository{db: db, queries: sqlcgen.New(db)}
}

// Save persists a health check session and its responses (atomic operation)
func (r *HealthCheckRepository) Save(ctx context.Context, session *healthcheck.HealthCheckSession) error {
	// Default survey type to "individual" if empty
	surveyType := session.SurveyType
	if surveyType == "" {
		surveyType = healthcheck.SurveyTypeIndividual
	}

	// Begin transaction for atomic save
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	qtx := r.queries.WithTx(tx)

	// Insert or update session
	err = qtx.UpsertHealthCheckSession(ctx, sqlcgen.UpsertHealthCheckSessionParams{
		ID:               session.ID,
		TeamID:           session.TeamID,
		UserID:           session.UserID,
		Date:             session.Date,
		AssessmentPeriod: sql.NullString{String: session.AssessmentPeriod, Valid: true},
		SurveyType:       surveyType,
		Completed:        session.Completed,
	})
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	// Delete existing responses (for updates)
	if err := qtx.DeleteHealthCheckResponsesBySessionID(ctx, session.ID); err != nil {
		return fmt.Errorf("failed to delete existing responses: %w", err)
	}

	// Insert responses
	for _, response := range session.Responses {
		err = qtx.InsertHealthCheckResponse(ctx, sqlcgen.InsertHealthCheckResponseParams{
			SessionID:   session.ID,
			DimensionID: response.DimensionID,
			Score:       int16(response.Score),
			Trend:       response.Trend,
			Comment:     sql.NullString{String: response.Comment, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("failed to save response: %w", err)
		}
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// FindByID retrieves a session by ID
func (r *HealthCheckRepository) FindByID(ctx context.Context, id string) (*healthcheck.HealthCheckSession, error) {
	rows, err := r.queries.FindSessionRowsByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	sessions := buildSessions(sessionScanRowsFromByID(rows))
	if len(sessions) == 0 {
		return nil, fmt.Errorf("session not found: %s", id)
	}

	return sessions[0], nil
}

// FindByTeamID retrieves all sessions for a team
func (r *HealthCheckRepository) FindByTeamID(ctx context.Context, teamID string) ([]*healthcheck.HealthCheckSession, error) {
	rows, err := r.queries.FindSessionRowsByTeamID(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	return buildSessions(sessionScanRowsFromByTeamID(rows)), nil
}

// FindByUserID retrieves all sessions for a user
func (r *HealthCheckRepository) FindByUserID(ctx context.Context, userID string) ([]*healthcheck.HealthCheckSession, error) {
	rows, err := r.queries.FindSessionRowsByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	return buildSessions(sessionScanRowsFromByUserID(rows)), nil
}

// FindByAssessmentPeriod retrieves all sessions for an assessment period
func (r *HealthCheckRepository) FindByAssessmentPeriod(ctx context.Context, period string) ([]*healthcheck.HealthCheckSession, error) {
	rows, err := r.queries.FindSessionRowsByAssessmentPeriod(ctx, sql.NullString{String: period, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	return buildSessions(sessionScanRowsFromByAssessmentPeriod(rows)), nil
}

// FindTeamHealthByManager retrieves aggregated health data for teams under a manager.
// Prefers post-workshop sessions when available for a team+period, otherwise falls back to individual sessions.
func (r *HealthCheckRepository) FindTeamHealthByManager(ctx context.Context, managerID string, assessmentPeriod string) ([]healthcheck.TeamHealthSummary, error) {
	var teamRows []teamHealthScanRow

	// Use effective_sessions CTE to prefer post_workshop data when available
	if assessmentPeriod != "" {
		rows, err := r.queries.FindTeamHealthByManagerWithPeriod(ctx, sqlcgen.FindTeamHealthByManagerWithPeriodParams{
			ManagerID:        managerID,
			AssessmentPeriod: sql.NullString{String: assessmentPeriod, Valid: true},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query team health by manager: %w", err)
		}
		teamRows = make([]teamHealthScanRow, len(rows))
		for i, row := range rows {
			teamRows[i] = teamHealthScanRow{
				TeamID: row.TeamID, TeamName: row.TeamName, SubmissionCount: row.SubmissionCount,
				OverallHealth: row.OverallHealth, PostWorkshopStatus: row.PostWorkshopStatus,
				DimensionID: row.DimensionID, AvgScore: row.AvgScore, ResponseCount: row.ResponseCount,
			}
		}
	} else {
		rows, err := r.queries.FindTeamHealthByManagerAllPeriods(ctx, managerID)
		if err != nil {
			return nil, fmt.Errorf("failed to query team health by manager: %w", err)
		}
		teamRows = make([]teamHealthScanRow, len(rows))
		for i, row := range rows {
			teamRows[i] = teamHealthScanRow{
				TeamID: row.TeamID, TeamName: row.TeamName, SubmissionCount: row.SubmissionCount,
				OverallHealth: row.OverallHealth, PostWorkshopStatus: row.PostWorkshopStatus,
				DimensionID: row.DimensionID, AvgScore: row.AvgScore, ResponseCount: row.ResponseCount,
			}
		}
	}

	// Group results by team (order is already by overall_health ASC from query)
	teamsMap := make(map[string]*healthcheck.TeamHealthSummary)
	teamOrder := []string{}

	for _, row := range teamRows {
		team, exists := teamsMap[row.TeamID]
		if !exists {
			health := 0.0
			if row.OverallHealth.Valid {
				health = row.OverallHealth.Float64
			}
			team = &healthcheck.TeamHealthSummary{
				TeamID:             row.TeamID,
				TeamName:           row.TeamName,
				SubmissionCount:    int(row.SubmissionCount),
				OverallHealth:      health,
				Dimensions:         []healthcheck.DimensionSummary{},
				PostWorkshopStatus: row.PostWorkshopStatus,
			}
			teamsMap[row.TeamID] = team
			teamOrder = append(teamOrder, row.TeamID)
		}

		if row.DimensionID.Valid && row.AvgScore.Valid {
			team.Dimensions = append(team.Dimensions, healthcheck.DimensionSummary{
				DimensionID:   row.DimensionID.String,
				AvgScore:      row.AvgScore.Float64,
				ResponseCount: int(row.ResponseCount.Int64),
			})
		}
	}

	teams := make([]healthcheck.TeamHealthSummary, 0, len(teamOrder))
	for _, id := range teamOrder {
		teams = append(teams, *teamsMap[id])
	}

	return teams, nil
}

// FindAggregatedDimensionsByManager retrieves aggregated dimension data across all teams under a manager.
// Prefers post-workshop sessions when available for a team+period, otherwise falls back to individual sessions.
func (r *HealthCheckRepository) FindAggregatedDimensionsByManager(ctx context.Context, managerID string, assessmentPeriod string) ([]healthcheck.DimensionSummary, error) {
	dimensions := []healthcheck.DimensionSummary{}

	if assessmentPeriod != "" {
		rows, err := r.queries.FindAggregatedDimensionsByManagerWithPeriod(ctx, sqlcgen.FindAggregatedDimensionsByManagerWithPeriodParams{
			ManagerID:        managerID,
			AssessmentPeriod: sql.NullString{String: assessmentPeriod, Valid: true},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query aggregated dimensions by manager: %w", err)
		}
		for _, row := range rows {
			dimensions = append(dimensions, healthcheck.DimensionSummary{
				DimensionID:   row.DimensionID,
				AvgScore:      row.AvgScore,
				ResponseCount: int(row.ResponseCount),
			})
		}
	} else {
		rows, err := r.queries.FindAggregatedDimensionsByManagerAllPeriods(ctx, managerID)
		if err != nil {
			return nil, fmt.Errorf("failed to query aggregated dimensions by manager: %w", err)
		}
		for _, row := range rows {
			dimensions = append(dimensions, healthcheck.DimensionSummary{
				DimensionID:   row.DimensionID,
				AvgScore:      row.AvgScore,
				ResponseCount: int(row.ResponseCount),
			})
		}
	}

	return dimensions, nil
}

// GetTeamSubmissionStatus returns submission status for a team in a given assessment period
func (r *HealthCheckRepository) GetTeamSubmissionStatus(ctx context.Context, teamID string, assessmentPeriod string) (*healthcheck.TeamSubmissionStatus, error) {
	row, err := r.queries.GetTeamSubmissionStatus(ctx, sqlcgen.GetTeamSubmissionStatusParams{
		TeamID:           teamID,
		AssessmentPeriod: sql.NullString{String: assessmentPeriod, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query team submission status: %w", err)
	}

	totalMembers := int(row.TotalMembers)
	submittedMembers := int(row.SubmittedMembers)
	allSubmitted := totalMembers > 0 && submittedMembers >= totalMembers

	return &healthcheck.TeamSubmissionStatus{
		TeamID:             teamID,
		AssessmentPeriod:   assessmentPeriod,
		TotalMembers:       totalMembers,
		SubmittedMembers:   submittedMembers,
		AllSubmitted:       allSubmitted,
		PostWorkshopExists: row.PostWorkshopExists,
	}, nil
}

// Delete removes a session and its responses (cascade handled by DB)
func (r *HealthCheckRepository) Delete(ctx context.Context, id string) error {
	rowsAffected, err := r.queries.DeleteHealthCheckSession(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("session not found: %s", id)
	}

	return nil
}

// FindDistinctAssessmentPeriods returns all unique assessment periods from submitted sessions
func (r *HealthCheckRepository) FindDistinctAssessmentPeriods(ctx context.Context) ([]string, error) {
	rows, err := r.queries.FindDistinctAssessmentPeriods(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query assessment periods: %w", err)
	}

	periods := make([]string, 0, len(rows))
	for _, period := range rows {
		periods = append(periods, period.String)
	}

	return periods, nil
}

// Helper types and functions

// sessionScanRow is the common shape shared by every FindSessionRowsBy*Row
// generated type - sqlc generates a distinct struct per query even though
// FindSessionRowsByID/ByTeamID/ByUserID/ByAssessmentPeriod share one SELECT list.
type sessionScanRow struct {
	SessionID        string
	TeamID           string
	UserID           string
	Date             string
	AssessmentPeriod sql.NullString
	SurveyType       string
	Completed        bool
	DimensionID      sql.NullString
	Score            sql.NullInt16
	Trend            sql.NullString
	Comment          sql.NullString
}

func sessionScanRowsFromByID(rows []sqlcgen.FindSessionRowsByIDRow) []sessionScanRow {
	out := make([]sessionScanRow, len(rows))
	for i, row := range rows {
		out[i] = sessionScanRow{
			SessionID: row.ID, TeamID: row.TeamID, UserID: row.UserID, Date: row.Date,
			AssessmentPeriod: row.AssessmentPeriod, SurveyType: row.SurveyType, Completed: row.Completed,
			DimensionID: row.DimensionID, Score: row.Score, Trend: row.Trend, Comment: row.Comment,
		}
	}
	return out
}

func sessionScanRowsFromByTeamID(rows []sqlcgen.FindSessionRowsByTeamIDRow) []sessionScanRow {
	out := make([]sessionScanRow, len(rows))
	for i, row := range rows {
		out[i] = sessionScanRow{
			SessionID: row.ID, TeamID: row.TeamID, UserID: row.UserID, Date: row.Date,
			AssessmentPeriod: row.AssessmentPeriod, SurveyType: row.SurveyType, Completed: row.Completed,
			DimensionID: row.DimensionID, Score: row.Score, Trend: row.Trend, Comment: row.Comment,
		}
	}
	return out
}

func sessionScanRowsFromByUserID(rows []sqlcgen.FindSessionRowsByUserIDRow) []sessionScanRow {
	out := make([]sessionScanRow, len(rows))
	for i, row := range rows {
		out[i] = sessionScanRow{
			SessionID: row.ID, TeamID: row.TeamID, UserID: row.UserID, Date: row.Date,
			AssessmentPeriod: row.AssessmentPeriod, SurveyType: row.SurveyType, Completed: row.Completed,
			DimensionID: row.DimensionID, Score: row.Score, Trend: row.Trend, Comment: row.Comment,
		}
	}
	return out
}

func sessionScanRowsFromByAssessmentPeriod(rows []sqlcgen.FindSessionRowsByAssessmentPeriodRow) []sessionScanRow {
	out := make([]sessionScanRow, len(rows))
	for i, row := range rows {
		out[i] = sessionScanRow{
			SessionID: row.ID, TeamID: row.TeamID, UserID: row.UserID, Date: row.Date,
			AssessmentPeriod: row.AssessmentPeriod, SurveyType: row.SurveyType, Completed: row.Completed,
			DimensionID: row.DimensionID, Score: row.Score, Trend: row.Trend, Comment: row.Comment,
		}
	}
	return out
}

// buildSessions groups flat (session, response) rows produced by a LEFT JOIN
// into HealthCheckSession aggregates with their Responses populated -
// mirrors the original scanSessions helper's grouping logic.
func buildSessions(rows []sessionScanRow) []*healthcheck.HealthCheckSession {
	sessionsMap := make(map[string]*healthcheck.HealthCheckSession)
	sessionOrder := []string{}

	for _, row := range rows {
		session, exists := sessionsMap[row.SessionID]
		if !exists {
			st := row.SurveyType
			if st == "" {
				st = healthcheck.SurveyTypeIndividual
			}
			session = &healthcheck.HealthCheckSession{
				ID:               row.SessionID,
				TeamID:           row.TeamID,
				UserID:           row.UserID,
				Date:             row.Date,
				AssessmentPeriod: row.AssessmentPeriod.String,
				SurveyType:       st,
				Completed:        row.Completed,
				Responses:        []healthcheck.HealthCheckResponse{},
			}
			sessionsMap[row.SessionID] = session
			sessionOrder = append(sessionOrder, row.SessionID)
		}

		if row.DimensionID.Valid {
			session.Responses = append(session.Responses, healthcheck.HealthCheckResponse{
				DimensionID: row.DimensionID.String,
				Score:       int(row.Score.Int16),
				Trend:       row.Trend.String,
				Comment:     row.Comment.String,
			})
		}
	}

	sessions := make([]*healthcheck.HealthCheckSession, 0, len(sessionOrder))
	for _, id := range sessionOrder {
		sessions = append(sessions, sessionsMap[id])
	}

	return sessions
}

// teamHealthScanRow is the common shape shared by both
// FindTeamHealthByManagerWithPeriodRow and FindTeamHealthByManagerAllPeriodsRow.
type teamHealthScanRow struct {
	TeamID             string
	TeamName           string
	SubmissionCount    int64
	OverallHealth      sql.NullFloat64
	PostWorkshopStatus string
	DimensionID        sql.NullString
	AvgScore           sql.NullFloat64
	ResponseCount      sql.NullInt64
}
