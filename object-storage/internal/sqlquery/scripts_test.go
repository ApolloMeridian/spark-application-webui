package sqlquery

import (
	"slices"
	"testing"
	"time"
)

func TestSplitSparkScript(t *testing.T) {
	sql := "USE polaris.somewhere; SELECT 'one;two', `a;b`; /* outer /* ; */ */ -- ;\nSELECT 3; -- done"
	want := []string{"USE polaris.somewhere", "SELECT 'one;two', `a;b`", "/* outer /* ; */ */ -- ;\nSELECT 3"}
	if got, err := splitStatements(sql); err != nil || !slices.Equal(got, want) {
		t.Fatalf("splitStatements = %q, %v", got, err)
	}
	for _, source := range []string{"SELECT 'unclosed", "/* unclosed", "-- empty"} {
		if _, err := splitStatements(source); err == nil {
			t.Fatalf("invalid script accepted: %q", source)
		}
	}
}

func TestSQLRetentionDuration(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"30s": 30 * time.Second, "10m": 10 * time.Minute, "4h": 4 * time.Hour, "1d": 24 * time.Hour,
	} {
		if got, err := retentionDuration(value); err != nil || got != want {
			t.Errorf("retentionDuration(%s) = %v, %v", value, got, err)
		}
	}
	for _, value := range []string{"9999999999999d", "-1d", "one day"} {
		if _, err := retentionDuration(value); err == nil {
			t.Errorf("invalid duration accepted: %s", value)
		}
	}
}
