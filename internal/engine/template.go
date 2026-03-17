package engine

import (
	"bytes"
	"fmt"
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
	"math/rand/v2"
	"os"
	"strings"
	"text/template"
)

// IterationData holds the data available to the command templates
type IterationData struct {
	Iteration          int
	WorkerID           int
	Timestamp          string
	TimestampUnix      int64
	TimestampUnixMilli int64
	TimestampUnixNano  int64
	UUID               string
	UserVars           map[string]string
	Prev               *Result // Previous pipeline step result
}

// Env returns the value of an environment variable.
// Returns an empty string if the variable is not set.
func (d IterationData) Env(key string) string {
	return os.Getenv(key)
}

// Var returns the value of a user-provided variable from --var.
// If the variable is not found in UserVars, it falls back to environment variables.
// Returns an empty string if the variable is not set.
func (d IterationData) Var(key string) string {
	if val, ok := d.UserVars[key]; ok {
		return val
	}
	return os.Getenv(key)
}

// ValidateTemplate parses a template string to check for syntax errors.
// It returns a descriptive error if parsing fails.
func ValidateTemplate(name, tpl string) error {
	_, err := template.New(name).Funcs(template.FuncMap{
		"quote":      shellQuote,
		"randInt":    randInt,
		"randFloat":  randFloat,
		"randString": randString,
		"randChoice": randChoice,
		"randEmail":  randEmail,
		"randName":   randName,
		"randPhone":  randPhone,
		"uuid":       uuidFunc,
		"ulid":       ulidFunc,
	}).Parse(tpl)
	return err
}

// renderTemplate parses and executes a Go template string with the provided data
func renderTemplate(name string, tpl string, data IterationData) (string, error) {
	if tpl == "" {
		return "", nil
	}

	t, err := template.New(name).Funcs(template.FuncMap{
		"quote":      shellQuote,
		"randInt":    randInt,
		"randFloat":  randFloat,
		"randString": randString,
		"randChoice": randChoice,
		"randEmail":  randEmail,
		"randName":   randName,
		"randPhone":  randPhone,
		"uuid":       uuidFunc,
		"ulid":       ulidFunc,
	}).Parse(tpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// randInt returns a random integer between min and max inclusive.
func randInt(min, max int) int {
	if min > max {
		min, max = max, min
	}
	return rand.IntN(max-min+1) + min
}

// randFloat returns a random float between min and max with the specified precision.
func randFloat(min, max float64, precision int) string {
	if min > max {
		min, max = max, min
	}
	val := rand.Float64()*(max-min) + min
	format := fmt.Sprintf("%%.%df", precision)
	return fmt.Sprintf(format, val)
}

// randString returns a random alphanumeric string of the specified length.
func randString(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}

// randChoice returns a random string from the provided choices.
func randChoice(choices ...string) string {
	if len(choices) == 0 {
		return ""
	}
	return choices[rand.IntN(len(choices))]
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Wrap in single quotes, and escape existing single quotes
	// ' becomes '\''
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// uuidFunc returns a new RFC 4122 v4 UUID.
func uuidFunc() string {
	return uuid.New().String()
}

// ulidFunc returns a new lexicographically sortable ULID.
func ulidFunc() string {
	return ulid.Make().String()
}

// randName returns a plausible full name.
func randName() string {
	firstNames := []string{"James", "Mary", "Robert", "Patricia", "John", "Jennifer", "Michael", "Linda", "David", "Elizabeth", "William", "Barbara", "Richard", "Susan", "Joseph", "Jessica", "Thomas", "Sarah", "Christopher", "Karen"}
	lastNames := []string{"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis", "Rodriguez", "Martinez", "Hernandez", "Lopez", "Gonzalez", "Wilson", "Anderson", "Thomas", "Taylor", "Moore", "Jackson", "Martin"}
	return firstNames[rand.IntN(len(firstNames))] + " " + lastNames[rand.IntN(len(lastNames))]
}

// randEmail returns a syntactically valid email address.
func randEmail() string {
	domains := []string{"example.com", "test.org", "demo.net", "mail.io", "generic.biz"}
	name := strings.ReplaceAll(strings.ToLower(randName()), " ", ".")
	return fmt.Sprintf("%s.%d@%s", name, rand.IntN(1000), domains[rand.IntN(len(domains))])
}

// randPhone returns a phone number in common international format.
func randPhone() string {
	return fmt.Sprintf("+1-%03d-%03d-%04d", rand.IntN(900)+100, rand.IntN(900)+100, rand.IntN(10000))
}
