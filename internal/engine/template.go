package engine

import (
	"bufio"
	"bytes"
	crypto_hmac "crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
	"math/rand/v2"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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

// TemplateState stores shared state across multiple template renderings in a single run
type TemplateState struct {
	counters    sync.Map // name -> *atomic.Int64
	fileLines   sync.Map // filename -> []string (cached lines)
	fileCursors sync.Map // filename -> *atomic.Int64 (sequential cursor)
	mu          sync.Mutex
}

// NewTemplateState creates a new TemplateState
func NewTemplateState() *TemplateState {
	return &TemplateState{}
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
func ValidateTemplate(name, tpl string, state *TemplateState) error {
	_, err := template.New(name).Funcs(funcMap(IterationData{}, state)).Parse(tpl)
	return err
}

// renderTemplate parses and executes a Go template string with the provided data
func renderTemplate(name string, tpl string, data IterationData, state *TemplateState) (string, error) {
	if tpl == "" {
		return "", nil
	}

	t, err := template.New(name).Funcs(funcMap(data, state)).Parse(tpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func funcMap(data IterationData, state *TemplateState) template.FuncMap {
	return template.FuncMap{
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
		"seq":        func(start, end, step int) int { return seq(start, end, step, data.Iteration) },
		"counter":    func(name string) int64 { return counter(name, state) },
		"fileLine":   func(filename string) string { return fileLine(filename, state) },
		"fileLineAt": func(filename string, index int) string { return fileLineAt(filename, index, state) },
		// Encoding & crypto
		"jsonEncode": jsonEncodeFunc,
		"base64Enc":  base64EncFunc,
		"base64Dec":  base64DecFunc,
		"sha256":     sha256Func,
		"hmac":       hmacFunc,
		"urlEncode":  urlEncodeFunc,
		"toJSON":     toJSONFunc,
	}
}

// seq returns a value in a sequence based on the current iteration.
func seq(start, end, step, iteration int) int {
	if step == 0 {
		return start
	}
	val := start + (iteration * step)
	if step > 0 && val > end {
		return end
	}
	if step < 0 && val < end {
		return end
	}
	return val
}

// counter returns an incrementing number starting from 1 for each named counter.
func counter(name string, state *TemplateState) int64 {
	if state == nil {
		return 1
	}
	actual, _ := state.counters.LoadOrStore(name, &atomic.Int64{})
	c := actual.(*atomic.Int64)
	return c.Add(1)
}

// fileLine returns the next line from the file (sequential access).
func fileLine(filename string, state *TemplateState) string {
	if state == nil {
		return ""
	}
	lines, err := getCachedLines(filename, state)
	if err != nil || len(lines) == 0 {
		return ""
	}
	actual, _ := state.fileCursors.LoadOrStore(filename, &atomic.Int64{})
	cursor := actual.(*atomic.Int64)
	idx := cursor.Add(1) - 1
	return lines[idx%int64(len(lines))]
}

// fileLineAt returns the line at the specified index (1-based).
func fileLineAt(filename string, index int, state *TemplateState) string {
	if state == nil {
		return ""
	}
	lines, err := getCachedLines(filename, state)
	if err != nil || index < 1 || index > len(lines) {
		return ""
	}
	return lines[index-1]
}

func getCachedLines(filename string, state *TemplateState) ([]string, error) {
	if val, ok := state.fileLines.Load(filename); ok {
		return val.([]string), nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	// Double check after acquiring lock
	if val, ok := state.fileLines.Load(filename); ok {
		return val.([]string), nil
	}

	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	state.fileLines.Store(filename, lines)
	return lines, nil
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

// jsonEncodeFunc JSON-encodes a value and returns the resulting JSON string.
// If marshalling fails (e.g. a channel), it returns an empty string.
func jsonEncodeFunc(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// base64EncFunc returns the standard Base64 encoding of s.
func base64EncFunc(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// base64DecFunc decodes a standard Base64-encoded string.
// Returns an empty string if the input is invalid.
func base64DecFunc(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// sha256Func returns the lowercase hex SHA-256 digest of s.
func sha256Func(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum)
}

// hmacFunc returns the HMAC digest of msg using key.
// algo must be "sha256"; unsupported algorithms return an empty string.
func hmacFunc(algo, key, msg string) string {
	switch algo {
	case "sha256":
		mac := crypto_hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(msg))
		return fmt.Sprintf("%x", mac.Sum(nil))
	default:
		return ""
	}
}

// urlEncodeFunc returns the URL query-escaped form of s.
// Spaces become '+', and special characters are percent-encoded.
func urlEncodeFunc(s string) string {
	return url.QueryEscape(s)
}

// toJSONFunc returns the JSON representation of v.
// Returns an empty string if marshalling fails.
func toJSONFunc(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
