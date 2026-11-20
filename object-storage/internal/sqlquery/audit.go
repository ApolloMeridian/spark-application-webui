package sqlquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type statementFailure struct {
	phase string
	cause error
}

func (e statementFailure) Error() string { return e.cause.Error() }
func (e statementFailure) Unwrap() error { return e.cause }

func failedDuring(phase string, err error) error {
	if err == nil {
		return nil
	}
	return statementFailure{phase, err}
}

type scriptAuditCounts struct {
	Total          int `json:"total"`
	Attempted      int `json:"attempted"`
	EngineAccepted int `json:"engineAccepted"`
	Skipped        int `json:"skipped"`
	FailedStep     int `json:"failedStep,omitempty"`
}

type sqlAuditDetails struct {
	UserID               string             `json:"userId"`
	RequestFingerprint   string             `json:"requestFingerprint,omitempty"`
	StatementFingerprint string             `json:"statementFingerprint,omitempty"`
	Step                 int                `json:"step,omitempty"`
	OperationID          string             `json:"operationId,omitempty"`
	State                string             `json:"state"`
	DurationMS           int64              `json:"durationMs"`
	Phase                string             `json:"phase,omitempty"`
	Plan                 statementPlan      `json:"plan"`
	Script               *scriptAuditCounts `json:"script,omitempty"`
	Outcome              map[string]string  `json:"outcome,omitempty"`
	ObjectUUIDs          map[string]string  `json:"objectUuids,omitempty"`
	EngineSucceeded      bool               `json:"engineSucceeded,omitempty"`
}

func auditedObjectUUIDs(bindings string) map[string]string {
	result := map[string]string{}
	for _, binding := range strings.Split(bindings, ",") {
		name, identity, _ := strings.Cut(binding, "=")
		uid, kind, _ := strings.Cut(identity, ":")
		if resource := "polaris." + name; validTable(resource) && uuidPattern.MatchString(uid) && kind != "metadata" {
			result[resource] = uid
		}
	}
	return result
}

func snapshotOutcome(operation string, columns []column, values []json.RawMessage) map[string]string {
	if !slices.Contains([]string{"fast_forward", "rollback_to_snapshot", "rollback_to_timestamp",
		"set_current_snapshot", "cherrypick_snapshot", "publish_changes"}, operation) {
		return nil
	}
	result := map[string]string{}
	for i, col := range columns {
		switch col.Name {
		case "previous_ref", "updated_ref", "previous_snapshot_id", "current_snapshot_id", "source_snapshot_id":
			value := strings.Trim(string(values[i]), "\"")
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				result[col.Name] = strconv.FormatInt(n, 10)
			}
		}
	}
	return result
}

func auditSQL(ctx context.Context, tx pgx.Tx, x user, id, action, result string, event sqlAuditDetails, failure error) error {
	event.UserID = x.ID
	setting := event.Plan.description()
	if event.Plan.Kind == "" {
		setting = ""
	}
	detail := "state=" + event.State
	if event.Step > 0 {
		detail += fmt.Sprintf(" step=%d", event.Step)
	}
	if action == "SQL_FINISH" || action == "SQL_STATEMENT" {
		detail += fmt.Sprintf(" durationMs=%d", event.DurationMS)
	}
	if event.OperationID != "" {
		detail += " operation=" + event.OperationID
	}
	if event.EngineSucceeded {
		detail += " engineSucceeded=true"
	}
	if event.RequestFingerprint != "" {
		detail += " requestFingerprint=" + event.RequestFingerprint
	}
	if event.StatementFingerprint != "" {
		detail += " statementFingerprint=" + event.StatementFingerprint
	}
	if event.Script != nil {
		c := event.Script
		detail += fmt.Sprintf(" statements=%d attempted=%d engineAccepted=%d skipped=%d", c.Total, c.Attempted, c.EngineAccepted, c.Skipped)
		if c.FailedStep != 0 {
			detail += fmt.Sprintf(" failedStep=%d", c.FailedStep)
		}
	}
	keys := []string{"previous_ref", "updated_ref", "previous_snapshot_id", "current_snapshot_id", "source_snapshot_id"}
	for _, key := range keys {
		if value, ok := event.Outcome[key]; ok {
			detail += " " + key + "=" + value
		}
	}
	reason := ""
	if failure != nil {
		var stage statementFailure
		switch {
		case errors.Is(failure, context.DeadlineExceeded):
			event.Phase = "timeout"
		case errors.Is(failure, context.Canceled):
			event.Phase = "cancelled"
		case errors.As(failure, &stage):
			event.Phase = stage.phase
		default:
			event.Phase = "execution"
		}
		reason = failure.Error()
		if len(reason) > 512 {
			reason = strings.ToValidUTF8(reason[:512], "")
		}
		detail += " phase=" + event.Phase
	}
	if setting != "" {
		detail += " " + setting
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return insertAudit(ctx, tx, x, id, action, result, reason, detail, raw)
}
