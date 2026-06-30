package health

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

// Status indicates the severity of a health check result.
type Status string

const (
	StatusOK       Status = "ok"
	StatusWarning  Status = "warning"
	StatusCritical Status = "critical"
	StatusError    Status = "error"
)

// CheckName identifies a specific health check.
type CheckName string

const (
	CheckIndex       CheckName = "index"
	CheckConnection  CheckName = "connection"
	CheckVacuum      CheckName = "vacuum"
	CheckSequence    CheckName = "sequence"
	CheckReplication CheckName = "replication"
	CheckBuffer      CheckName = "buffer"
	CheckConstraint  CheckName = "constraint"
)

var allChecks = []CheckName{
	CheckIndex, CheckConnection, CheckVacuum,
	CheckSequence, CheckReplication, CheckBuffer, CheckConstraint,
}

// Result holds the output of a single health check.
type Result struct {
	Check   CheckName `json:"check"`
	Status  Status    `json:"status"`
	Message string    `json:"message"`
	Details any       `json:"details,omitempty"`
}

// AnalyzeHealth runs the requested health checks concurrently and returns results.
// checks may contain specific check names or "all" to run every check.
func AnalyzeHealth(ctx context.Context, d db.Querier, checks []string) ([]Result, error) {
	targets, err := resolveChecks(checks)
	if err != nil {
		return nil, err
	}

	results := make([]Result, len(targets))
	var wg sync.WaitGroup
	for i, c := range targets {
		wg.Add(1)
		go func(idx int, check CheckName) {
			defer wg.Done()
			results[idx] = runCheck(ctx, d, check)
		}(i, c)
	}
	wg.Wait()
	return results, nil
}

func resolveChecks(input []string) ([]CheckName, error) {
	if len(input) == 0 {
		return allChecks, nil
	}
	for _, s := range input {
		if strings.EqualFold(s, "all") {
			return allChecks, nil
		}
	}

	var out []CheckName
	for _, s := range input {
		switch CheckName(strings.ToLower(s)) {
		case CheckIndex:
			out = append(out, CheckIndex)
		case CheckConnection:
			out = append(out, CheckConnection)
		case CheckVacuum:
			out = append(out, CheckVacuum)
		case CheckSequence:
			out = append(out, CheckSequence)
		case CheckReplication:
			out = append(out, CheckReplication)
		case CheckBuffer:
			out = append(out, CheckBuffer)
		case CheckConstraint:
			out = append(out, CheckConstraint)
		default:
			return nil, fmt.Errorf("unknown health check %q; valid values: index, connection, vacuum, sequence, replication, buffer, constraint, all", s)
		}
	}
	return out, nil
}

func runCheck(ctx context.Context, d db.Querier, check CheckName) Result {
	switch check {
	case CheckIndex:
		return runIndexHealth(ctx, d)
	case CheckConnection:
		return runConnectionHealth(ctx, d)
	case CheckVacuum:
		return runVacuumHealth(ctx, d)
	case CheckSequence:
		return runSequenceHealth(ctx, d)
	case CheckReplication:
		return runReplicationHealth(ctx, d)
	case CheckBuffer:
		return runBufferHealth(ctx, d)
	case CheckConstraint:
		return runConstraintHealth(ctx, d)
	default:
		return Result{Check: check, Status: StatusError, Message: "unknown check"}
	}
}
