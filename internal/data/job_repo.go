package data

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"seedwright/internal/data/model"
)

// JobRecord represents a persisted job in SQLite.
type JobRecord struct {
	ID              string         `db:"id"`
	ElementID       string         `db:"element_id"`
	Project         string         `db:"project"`
	SDCPPJobID      string         `db:"sdcpp_job_id"`
	Status          string         `db:"status"` // queued, generating, completed, failed, cancelled
	ErrorMessage    sql.NullString `db:"error_msg"`
	SDCPPStarted    sql.NullTime   `db:"sdcpp_started"`
	SDCPPCompleted  sql.NullTime   `db:"sdcpp_completed"`
}

// JobInfoJSON is the JSON-serializable shape for job API responses.
type JobInfoJSON struct {
	ID           string         `json:"id"`
	ElementID    string         `json:"element_id"`
	Project      string         `json:"project"`
	SDCPPJobID   string         `json:"sdcpp_job_id"`
	Status       string         `json:"status"`
	ErrorMessage string         `json:"error_message,omitempty"`
	StartedAt    *time.Time     `json:"started_at,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
}

// ToJSON converts a JobRecord to its JSON-serializable form with snake_case field names.
func (r JobRecord) ToJSON() JobInfoJSON {
	return JobInfoJSON{
		ID:           r.ID,
		ElementID:    r.ElementID,
		Project:      r.Project,
		SDCPPJobID:   r.SDCPPJobID,
		Status:       r.Status,
		ErrorMessage: r.ErrorMessage.String,
		StartedAt:    nilTime(r.SDCPPStarted),
		CompletedAt:  nilTime(r.SDCPPCompleted),
	}
}

// JobRepository handles persistence of job records in SQLite.
type JobRepository interface {
	// CreateJob inserts a new job record.
	CreateJob(ctx context.Context, record JobRecord) error

	// GetJob returns a job by ID.
	GetJob(ctx context.Context, id string) (JobRecord, error)

	// GetLatestJobByElement returns the most recent job for an element.
	GetLatestJobByElement(ctx context.Context, elementID string) (JobRecord, error)

	// ListActiveJobs returns jobs in queued or generating status for a project.
	ListActiveJobs(ctx context.Context, project string) ([]JobRecord, error)

	// ListStuckJobs returns jobs that are stuck (queued/generating with no sdcpp_job_id).
	ListStuckJobs(ctx context.Context, project string) ([]JobRecord, error)

	// UpdateStatus updates the status, sdcpp timestamps, and optional error for a job.
	UpdateStatus(ctx context.Context, id, status string, errMsg sql.NullString, sdcppCompleted sql.NullTime) error

	// UpdateSDCPPJobID stores the sdcpp job ID after submitJob returns.
	UpdateSDCPPJobID(ctx context.Context, id, sdcppJobID string) error

	// DeleteJob removes a job row (used for terminal jobs — runtime-only cleanup).
	DeleteJob(ctx context.Context, id string) error
}

type jobRepo struct {
	db *sql.DB
}

// NewJobRepository creates a new JobRepository.
func NewJobRepository(db *sql.DB) JobRepository {
	return &jobRepo{db: db}
}

func (r *jobRepo) CreateJob(ctx context.Context, record JobRecord) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO jobs (id, element_id, project, sdcpp_job_id, status)
		 VALUES (?, ?, ?, ?, ?)`,
		record.ID, record.ElementID, record.Project, record.SDCPPJobID, record.Status,
	)
	return err
}

func (r *jobRepo) GetJob(ctx context.Context, id string) (JobRecord, error) {
	var rec JobRecord
	err := r.db.QueryRowContext(ctx,
		`SELECT id, element_id, project, sdcpp_job_id, status, error_msg,
		        sdcpp_started, sdcpp_completed
		 FROM jobs WHERE id = ?`, id,
	).Scan(
		&rec.ID, &rec.ElementID, &rec.Project, &rec.SDCPPJobID,
		&rec.Status, &rec.ErrorMessage, &rec.SDCPPStarted, &rec.SDCPPCompleted,
	)
	if err != nil {
		return JobRecord{}, fmt.Errorf("get job %s: %w", id, err)
	}
	return rec, nil
}

func (r *jobRepo) GetLatestJobByElement(ctx context.Context, elementID string) (JobRecord, error) {
	var rec JobRecord
	err := r.db.QueryRowContext(ctx,
		`SELECT id, element_id, project, sdcpp_job_id, status, error_msg,
		        sdcpp_started, sdcpp_completed
		 FROM jobs WHERE element_id = ?
		 ORDER BY id DESC LIMIT 1`, elementID,
	).Scan(
		&rec.ID, &rec.ElementID, &rec.Project, &rec.SDCPPJobID,
		&rec.Status, &rec.ErrorMessage, &rec.SDCPPStarted, &rec.SDCPPCompleted,
	)
	if err != nil {
		return JobRecord{}, fmt.Errorf("get latest job by element %s: %w", elementID, err)
	}
	return rec, nil
}

func (r *jobRepo) ListActiveJobs(ctx context.Context, project string) ([]JobRecord, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, element_id, project, sdcpp_job_id, status, error_msg,
		        sdcpp_started, sdcpp_completed
		 FROM jobs WHERE project = ? AND status IN ('queued', 'generating')
		 ORDER BY id DESC`, project,
	)
	if err != nil {
		return nil, fmt.Errorf("list active jobs: %w", err)
	}
	defer rows.Close()

	return scanJobRecords(rows)
}

func (r *jobRepo) UpdateStatus(ctx context.Context, id, status string, errMsg sql.NullString, sdcppCompleted sql.NullTime) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error_msg = ?, sdcpp_completed = ?
		 WHERE id = ?`, status, errMsg, sdcppCompleted, id,
	)
	return err
}

func (r *jobRepo) UpdateSDCPPJobID(ctx context.Context, id, sdcppJobID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET sdcpp_job_id = ? WHERE id = ?`, sdcppJobID, id,
	)
	return err
}

func (r *jobRepo) ListStuckJobs(ctx context.Context, project string) ([]JobRecord, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, element_id, project, sdcpp_job_id, status, error_msg,
		        sdcpp_started, sdcpp_completed
		 FROM jobs WHERE project = ? AND status IN ('queued', 'generating')
		 ORDER BY id DESC`, project,
	)
	if err != nil {
		return nil, fmt.Errorf("list stuck jobs: %w", err)
	}
	defer rows.Close()

	return scanJobRecords(rows)
}

func (r *jobRepo) DeleteJob(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM jobs WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	return nil
}

func scanJobRecords(rows *sql.Rows) ([]JobRecord, error) {
	var records []JobRecord
	for rows.Next() {
		var rec JobRecord
		err := rows.Scan(
			&rec.ID, &rec.ElementID, &rec.Project, &rec.SDCPPJobID,
			&rec.Status, &rec.ErrorMessage, &rec.SDCPPStarted, &rec.SDCPPCompleted,
		)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

// FromDomain creates a JobRecord from domain values.
// Each job gets a fresh UUID as its ID — distinct from element_id so that
// multiple job submissions for the same element (regenerate-in-place,
// generate-clone) can coexist in the jobs table.
func FromDomain(elem model.Element, sdcppJobID, status string) JobRecord {
	rec := JobRecord{
		ID:         uuid.New().String(), // unique job submission ID
		ElementID:  elem.ID,              // the element this job belongs to
		Project:    elem.Project,
		SDCPPJobID: sdcppJobID,
		Status:     status,
	}
	return rec
}

func nilTime(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	t := nt.Time
	return &t
}
