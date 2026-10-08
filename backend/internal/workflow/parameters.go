package workflow

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
	"sigs.k8s.io/yaml"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/service"
)

var dateExpression = regexp.MustCompile(`\$\[([^\]]+)\]`)
var dateOffset = regexp.MustCompile(`^(.*?)([+-]\d+)(?:/(\d+))?$`)
var dateToken = regexp.MustCompile(`yyyy|SSS|MM|dd|HH|mm|ss`)

// Integer day offsets use calendar arithmetic; fractions use elapsed hours.
// The baseline is always the persisted logical schedule time, never polling time.
func ExpandDates(value string, baseline time.Time, zone string) (string, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", err
	}
	var failure error
	result := dateExpression.ReplaceAllStringFunc(value, func(expr string) string {
		format := expr[2 : len(expr)-1]
		target := baseline.In(loc)
		if parts := dateOffset.FindStringSubmatch(format); parts != nil {
			format = parts[1]
			offset, e := strconv.Atoi(parts[2])
			if e != nil || offset > 36600 || offset < -36600 {
				failure = fmt.Errorf("invalid date offset: %s", expr)
				return ""
			}
			if parts[3] == "" {
				target = target.AddDate(0, 0, offset)
			} else {
				divisor, e := strconv.Atoi(parts[3])
				if e != nil || divisor < 1 || divisor > 86400 {
					failure = fmt.Errorf("invalid date divisor: %s", expr)
					return ""
				}
				target = target.Add(time.Duration(float64(offset) * float64(24*time.Hour) / float64(divisor)))
			}
		}
		if !strings.Contains(format, "yyyy") {
			failure = fmt.Errorf("date format must include yyyy: %s", expr)
			return ""
		}
		return dateToken.ReplaceAllStringFunc(format, func(token string) string {
			switch token {
			case "yyyy":
				return target.Format("2006")
			case "MM":
				return target.Format("01")
			case "dd":
				return target.Format("02")
			case "HH":
				return target.Format("15")
			case "mm":
				return target.Format("04")
			case "ss":
				return target.Format("05")
			default:
				return fmt.Sprintf("%03d", target.Nanosecond()/1000000)
			}
		})
	})
	if strings.Contains(result, "$[") {
		return "", fmt.Errorf("malformed date expression")
	}
	return result, failure
}

func ParseCron(expression, zone string) (cron.Schedule, error) {
	if count := len(strings.Fields(expression)); count != 5 && count != 6 {
		return nil, fmt.Errorf("cron requires six fields (second minute hour day month weekday), or five legacy fields")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return nil, err
	}
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return parser.Parse("CRON_TZ=" + zone + " " + expression)
}
func CronPreview(expression, zone string, from time.Time, count int) ([]string, error) {
	return CronPreviewWindow(expression, zone, from, time.Time{}, time.Time{}, count)
}
func CronPreviewWindow(expression, zone string, from, start, end time.Time, count int) ([]string, error) {
	schedule, err := ParseCron(expression, zone)
	if err != nil {
		return nil, err
	}
	if count < 1 || count > 200 {
		return nil, fmt.Errorf("preview count must be 1-200")
	}
	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		return nil, fmt.Errorf("invalid schedule range or Cron: end precedes start")
	}
	if !start.IsZero() && !start.Before(from) {
		from = start.Add(-time.Nanosecond)
	}
	result := []string{}
	for i := 0; i < count; i++ {
		from = schedule.Next(from)
		if from.IsZero() {
			break
		}
		if !end.IsZero() && from.After(end) {
			break
		}
		result = append(result, from.UTC().Format(time.RFC3339Nano))
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("invalid schedule range or Cron: no execution occurs within the valid future window")
	}
	return result, nil
}

// Binary-search the last missed occurrence. With second-level Cron a linear
// scan across long downtime could otherwise hold the dispatch lock for hours.
func latestOccurrence(schedule cron.Schedule, due, now time.Time) time.Time {
	if due.IsZero() || due.After(now) {
		return time.Time{}
	}
	low, high := due.Unix()-1, now.Unix()
	for high-low > 1 {
		middle := low + (high-low)/2
		next := schedule.Next(time.Unix(middle, 0))
		if !next.IsZero() && !next.After(now) {
			low = middle
		} else {
			high = middle
		}
	}
	return schedule.Next(time.Unix(low, 0))
}

func RenderNode(node domain.WorkflowNode, baseline time.Time, zone, taskID string, attempt int) (string, string, error) {
	values := map[string]string{}
	for _, p := range node.Parameters {
		values[p.Name] = p.Default
	}
	for k, v := range node.Values {
		if _, declared := values[k]; !declared {
			return "", "", fmt.Errorf("unknown task parameter: %s", k)
		}
		values[k] = v
	}
	for k, v := range values {
		expanded, err := ExpandDates(v, baseline, zone)
		if err != nil {
			return "", "", err
		}
		values[k] = expanded
	}
	manifest, err := service.RenderManifest(node.Manifest, node.Namespace, node.Parameters, values)
	if err != nil {
		return "", "", err
	}
	manifest, err = ExpandDates(manifest, baseline, zone)
	if err != nil {
		return "", "", err
	}
	var object map[string]any
	if err = yaml.UnmarshalStrict([]byte(manifest), &object); err != nil {
		return "", "", err
	}
	metadata, _ := object["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	if node.NameSuffix == nil || *node.NameSuffix {
		unique := strings.TrimPrefix(taskID, "task-")
		if len(unique) > 16 {
			unique = unique[len(unique)-16:]
		}
		suffix := "-" + strings.ReplaceAll(baseline.UTC().Format("20060102150405.000"), ".", "") + "-" + unique + fmt.Sprintf("-%d", attempt)
		if len(name) > 63-len(suffix) {
			name = name[:63-len(suffix)]
		}
		name = strings.TrimRight(name, "-") + suffix
	}
	if !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(name) || len(name) > 63 {
		return "", "", fmt.Errorf("rendered metadata.name must be a DNS label of at most 63 characters")
	}
	metadata["name"] = name
	annotations, _ := metadata["annotations"].(map[string]any)
	if annotations == nil {
		annotations = map[string]any{}
	}
	annotations["spark-control-center.io/task-instance"] = taskID
	metadata["annotations"] = annotations
	encoded, err := yaml.Marshal(object)
	return string(encoded), name, err
}
