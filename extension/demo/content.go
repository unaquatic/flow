package demo

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
)

// Segment is the reply of one step of the demo: text and an optional
// simulated tool call, which Model streams as a model's response.
type Segment struct {
	// Text is streamed before the tool call.
	Text string
	// Tool supplies a canned result instead of executing a real tool.
	Tool *Tool
}

// Tool holds the input and result of a simulated call.
type Tool struct {
	// Name identifies the simulated tool.
	Name string
	// Input contains the tool's complete JSON arguments.
	Input json.RawMessage
	// Output is the simulated tool result, which the call returns when it
	// ends.
	Output string
	// Error, when set, fails the call with the error the tool reports, such
	// as agent.FailedToolRun's text for a failed run or agent.ToolFailure's
	// content for a failed tool.
	Error string
}

var Prompts = []string{
	"Add a Divide method to the Calculator with zero-division handling",
	"Add tests for division and run the calculator test suite",
	"Review the calculator implementation and verify its error handling",
}

// Segments are the steps of the demo reply. Its calls use every built-in
// tool and end in every way a real call ends: completed, a run that exits
// nonzero with and without output, times out, or is cancelled, a tool that
// fails, a tool that is not found, and a call that is interrupted. Its bash
// calls include a long command, a multi-line one, and one with a timeout; a
// read call reads part of a file, and a grep call searches below a path.
// Its first reply streams a table, and near its end a reply streams a code
// block taller than most screens, which inline mode shows, as every block of
// a reply, once each is whole, a write call streams a long file, and a bash
// call returns long output, which inline mode shows whole only once each
// call has ended. Its last reply, summary, shows every kind of Markdown the
// chat renders.
var Segments = []Segment{
	{
		Text: "Division has to get these cases right:\n\n" + DivideCasesTable() + "\n\nI'll check which Go version the module targets.",
		Tool: &Tool{
			Name: "read", Input: json.RawMessage(`{"path":"go.mod"}`),
			Output: "module example/calculator\n\ngo 1.27\n",
		},
	},
	{
		Text: "I'll inspect the calculator package, add division with an explicit error for a zero divisor, then write tests and run them.\n\n1. Find the source and tests.\n2. Read the existing implementation.\n3. Add the method and regression tests.\n4. Run the package tests.",
		Tool: &Tool{
			Name: "glob", Input: json.RawMessage(`{"pattern":"calculator/*.go"}`),
			Output: "calculator/calc.go\ncalculator/calc_test.go\n",
		},
	},
	{
		Text: "I'll track the plan as a todo list.",
		Tool: &Tool{
			Name:   "todo",
			Input:  json.RawMessage(`{"todos":[{"content":"Find the source and tests","status":"completed"},{"content":"Read the existing implementation","status":"in_progress"},{"content":"Add the method and regression tests","status":"pending"},{"content":"Run the package tests","status":"pending"}]}`),
			Output: "[✓] Find the source and tests\n[–] Read the existing implementation\n[ ] Add the method and regression tests\n[ ] Run the package tests",
		},
	},
	{
		Text: "The package has a source file and a test file. I'll find the existing methods to match their style.",
		Tool: &Tool{
			Name: "grep", Input: json.RawMessage(`{"pattern":"^func \\(c \\*Calculator\\)","path":"calculator","include":"*.go"}`),
			Output: "calc.go:7:func (c *Calculator) Add(a, b float64) float64 {\ncalc.go:12:func (c *Calculator) Subtract(a, b float64) float64 {\ncalc.go:17:func (c *Calculator) Multiply(a, b float64) float64 {\n",
		},
	},
	{
		Text: "Every method lives in `calc.go`. I'll check whether a divide file exists already.",
		Tool: &Tool{
			Name: "read", Input: json.RawMessage(`{"path":"calculator/divide.go"}`),
			Error: agent.ToolFailure("", "not found: calculator/divide.go").Content,
		},
	},
	{
		Text: "There is no divide file, so `Divide` belongs in `calc.go`. I'll read it.",
		Tool: &Tool{
			Name: "read", Input: json.RawMessage(`{"path":"calculator/calc.go"}`),
			Output: calculatorSource,
		},
	},
	{
		Text: "The calculator is stateless, and its methods return plain values. I'll check the `errors` package docs for the way to report a zero divisor.",
		Tool: &Tool{
			Name: "webfetch", Input: json.RawMessage(`{"url":"https://pkg.go.dev/errors"}`),
			Error: agent.ToolFailure("", fmt.Sprintf("%v: %s", agent.ErrToolNotFound, "webfetch")).Content,
		},
	},
	{
		Text: "Fetching pages is not available here, and `errors.New` is enough. I'll add the import next to the existing ones.",
		Tool: &Tool{
			Name: "edit", Input: json.RawMessage(`{"path":"calculator/calc.go","old_string":"package calculator\n\nimport \"fmt\"","new_string":"package calculator\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n)"}`),
			Error: agent.ToolFailure("", "old_string not found").Content,
		},
	},
	{
		Text: "`calc.go` has no imports yet, so I'll add the import after the package clause.",
		Tool: &Tool{
			Name: "edit", Input: json.RawMessage(`{"path":"calculator/calc.go","old_string":"package calculator","new_string":"package calculator\n\nimport \"errors\""}`),
			Output: "Updated calculator/calc.go\n",
		},
	},
	{
		Tool: &Tool{
			Name: "edit", Input: json.RawMessage(`{"path":"calculator/calc.go","old_string":"type Calculator struct{}","new_string":"type Calculator struct{}\n\n// Divide returns the quotient, or an error if b is zero.\nfunc (c *Calculator) Divide(a, b float64) (float64, error) {\n\tif b == 0 {\n\t\treturn 0, errors.New(\"division by zero\")\n\t}\n\treturn a / b, nil\n}"}`),
			Output: "Updated calculator/calc.go\n",
		},
	},
	{
		Text: "I'll read the new method back in place.",
		Tool: &Tool{
			Name: "read", Input: json.RawMessage(`{"path":"calculator/calc.go","offset":8,"limit":7}`),
			Output: divideSource,
		},
	},
	{
		Text: "The method returns `(float64, error)` and rejects a zero divisor without changing the other operations. I'll check the existing test style before adding coverage.",
		Tool: &Tool{
			Name: "read", Input: json.RawMessage(`{"path":"calculator/calc_test.go"}`),
			Output: calculatorTests,
		},
	},
	{
		Text: "I'll add a separate test file for successful division and the zero-divisor error. The assertions will check both return values.",
		Tool: &Tool{
			Name: "write", Input: json.RawMessage(`{"path":"calculator/divide_test.go","content":"package calculator\n\nimport \"testing\"\n\nfunc TestDivide(t *testing.T) {\n\tc := &Calculator{}\n\tgot, err := c.Divide(7, 2)\n\tif err != nil || got != 3 {\n\t\tt.Fatalf(\"Divide(7, 2) = %v, %v; want 3, nil\", got, err)\n\t}\n}\n\nfunc TestDivideByZero(t *testing.T) {\n\tc := &Calculator{}\n\tgot, err := c.Divide(6, 0)\n\tif got != 0 || err == nil {\n\t\tt.Fatalf(\"Divide(6, 0) = %v, %v; want 0 and an error\", got, err)\n\t}\n}\n"}`),
			Output: "Wrote calculator/divide_test.go\n",
		},
	},
	{
		Text: "I'll run just the new tests first.",
		Tool: &Tool{
			Name: "bash", Input: json.RawMessage(`{"command":"go test ./calculator/... -run 'TestDivide|TestDivideByZero' -count=1 -v","description":"Run the divide tests"}`),
			Output: divideRun,
			Error:  agent.FailedToolRun(divideRun, "exit 1").Text,
		},
	},
	{
		Text: "`TestDivide` expected integer division, but `Divide` returns a `float64`. I'll fix the expectation.",
		Tool: &Tool{
			Name: "edit", Input: json.RawMessage(`{"path":"calculator/divide_test.go","old_string":"got != 3 {\n\t\tt.Fatalf(\"Divide(7, 2) = %v, %v; want 3, nil\"","new_string":"got != 3.5 {\n\t\tt.Fatalf(\"Divide(7, 2) = %v, %v; want 3.5, nil\""}`),
			Output: "Updated calculator/divide_test.go\n",
		},
	},
	{
		Text: "I'll run the whole package with verbose output.",
		Tool: &Tool{
			Name: "bash", Input: json.RawMessage(`{"command":"go test ./calculator/... -v","description":"Run calculator tests"}`),
			Output: "=== RUN   TestAdd\n--- PASS: TestAdd (0.00s)\n=== RUN   TestDivide\n--- PASS: TestDivide (0.00s)\n=== RUN   TestDivideByZero\n--- PASS: TestDivideByZero (0.00s)\nPASS\nok\texample/calculator\t0.003s\n",
		},
	},
	{
		Text: "The tests pass. I'll check that gofmt has nothing to rewrite.",
		Tool: &Tool{
			Name: "bash", Input: json.RawMessage(`{"command":"cd calculator\ntest -z \"$(gofmt -l .)\"","description":"Check that gofmt is clean"}`),
			Error: agent.FailedToolRun("", "exit 1").Text,
		},
	},
	{
		Text: "gofmt lists a file to rewrite. I'll format the package and list what is left.",
		Tool: &Tool{
			Name: "bash", Input: json.RawMessage(`{"command":"gofmt -w calculator && gofmt -l calculator","description":"Format the calculator package"}`),
			Output: "[no output]",
		},
	},
	{
		Text: "Nothing is left to format. I'll list the package for stray files.",
		Tool: &Tool{
			Name: "ls", Input: json.RawMessage(`{"path":"calculator"}`),
			Error: message.InterruptedError,
		},
	},
	{
		Text: "That listing was interrupted before it ran. I'll list the package again.",
		Tool: &Tool{
			Name: "ls", Input: json.RawMessage(`{"path":"calculator"}`),
			Output: "calc.go\ncalc_test.go\ndivide_test.go\n",
		},
	},
	{
		Text: "Only the expected files are there. I'll benchmark `Divide` under a 2-minute timeout.",
		Tool: &Tool{
			Name: "bash", Input: json.RawMessage(`{"command":"go test ./calculator/... -run '^$' -bench Divide -benchtime 5m","description":"Benchmark Divide","timeout_ms":120000}`),
			Output: benchRun,
			Error:  agent.FailedToolRun(benchRun, "timeout after 120s").Text,
		},
	},
	{
		Text: "A 5-minute benchmark cannot finish in 2 minutes, and this change does not need one. I'll start a watcher that reruns the tests on save.",
		Tool: &Tool{
			Name: "bash", Input: json.RawMessage(`{"command":"watchexec -e go -- go test ./calculator/...","description":"Rerun the tests on save"}`),
			Output: watchRun,
			Error:  agent.FailedToolRun(watchRun, "cancelled").Text,
		},
	},
	{
		Text: "The watcher was cancelled: it runs until it is stopped, and the tests already pass. Two tests leave most inputs unchecked, so I'll add a table of cases.",
		Tool: &Tool{
			Name: "write", Input: CallInput(struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}{"calculator/divide_cases_test.go", DivideCasesSource()}),
			Output: "Wrote calculator/divide_cases_test.go\n",
		},
	},
	{
		Text: "I'll run every case.",
		Tool: &Tool{
			Name: "bash", Input: json.RawMessage(`{"command":"go test ./calculator/... -run TestDivideCases -count=1 -v","description":"Run the division cases"}`),
			Output: DivideCasesRun(),
		},
	},
	{
		Text: "Every case passes. Here is `calc.go` as it stands now:\n\n```go\n" + finalSource + "```\n\nI'll check that the tests cover every statement.",
		Tool: &Tool{
			Name: "bash", Input: json.RawMessage(`{"command":"go test ./calculator/... -cover","description":"Check test coverage"}`),
			Output: "ok  \texample/calculator\t0.004s\tcoverage: 100.0% of statements\n",
		},
	},
	{Text: summary},
}

// CallInput returns v as the JSON input of a call, its fields in the order
// v lists them, and its text as written: with no character escaped for
// HTML. It panics when v cannot be encoded, as the demo's content always
// can.
func CallInput(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// DivideCase is a case of the demo's table-driven test of Divide.
type DivideCase struct {
	// Name names the case.
	Name string
	// A and B are the dividend and the divisor, as Go writes them.
	A, B string
	// Want is the quotient, as Go writes it, or empty for the error of a
	// zero divisor.
	Want string
}

// DivideCases are the cases the demo's table-driven test of Divide checks,
// the cases of a zero divisor last.
var DivideCases = []DivideCase{
	{"whole", "8", "2", "4"},
	{"fraction", "7", "2", "3.5"},
	{"negative divisor", "9", "-3", "-3"},
	{"negative dividend", "-9", "3", "-3"},
	{"both negative", "-9", "-3", "3"},
	{"zero dividend", "0", "5", "0"},
	{"less than one", "1", "4", "0.25"},
	{"large", "1e12", "1e3", "1e9"},
	{"zero divisor", "6", "0", ""},
	{"zero by zero", "0", "0", ""},
}

// DivideCasesSource returns the file of the demo's table-driven test, which
// checks each of DivideCases.
func DivideCasesSource() string {
	var cases strings.Builder
	for _, c := range DivideCases {
		fmt.Fprintf(&cases, "\t\t{%q, %s, %s, %s, %v},\n", c.Name, c.A, c.B, cmp.Or(c.Want, "0"), c.Want == "")
	}
	return "package calculator\n\nimport \"testing\"\n\n" +
		"func TestDivideCases(t *testing.T) {\n" +
		"\tcases := []struct {\n" +
		"\t\tname    string\n" +
		"\t\ta, b    float64\n" +
		"\t\twant    float64\n" +
		"\t\twantErr bool\n" +
		"\t}{\n" +
		cases.String() +
		"\t}\n" +
		"\tc := &Calculator{}\n" +
		"\tfor _, tc := range cases {\n" +
		"\t\tt.Run(tc.name, func(t *testing.T) {\n" +
		"\t\t\tgot, err := c.Divide(tc.a, tc.b)\n" +
		"\t\t\tif (err != nil) != tc.wantErr || got != tc.want {\n" +
		"\t\t\t\tt.Fatalf(\"Divide(%v, %v) = %v, %v; want %v, error %v\", tc.a, tc.b, got, err, tc.want, tc.wantErr)\n" +
		"\t\t\t}\n" +
		"\t\t})\n" +
		"\t}\n" +
		"}\n"
}

// DivideCasesRun returns the verbose output of a run of the demo's
// table-driven test, in which each of DivideCases passes.
func DivideCasesRun() string {
	var run, passed strings.Builder
	for _, c := range DivideCases {
		name := "TestDivideCases/" + strings.ReplaceAll(c.Name, " ", "_")
		fmt.Fprintf(&run, "=== RUN   %s\n", name)
		fmt.Fprintf(&passed, "    --- PASS: %s (0.00s)\n", name)
	}
	return "=== RUN   TestDivideCases\n" + run.String() +
		"--- PASS: TestDivideCases (0.00s)\n" + passed.String() +
		"PASS\nok  \texample/calculator\t0.004s\n"
}

// DivideCasesTable returns DivideCases as a Markdown table: each case, its
// operands, and the quotient it expects, or the error of a zero divisor,
// which widens the last column only in the table's last rows.
func DivideCasesTable() string {
	rows := []string{"| Case | a | b | Expected |", "|------|--:|--:|----------|"}
	for _, c := range DivideCases {
		rows = append(rows, fmt.Sprintf("| %s | %s | %s | %s |", c.Name, c.A, c.B, cmp.Or(c.Want, "error: `division by zero`")))
	}
	return strings.Join(rows, "\n")
}

// summary is the demo's last reply: a summary of the change, and then a
// check that shows every kind of Markdown the chat renders.
var summary = strings.Join([]string{
	"Every statement is covered.",
	"",
	"## Summary",
	"",
	"| File | Change |",
	"|------|--------|",
	"| `calculator/calc.go` | Added division and zero-divisor handling |",
	"| `calculator/divide_test.go` | Added success and error tests |",
	"| `calculator/divide_cases_test.go` | Added a table of division cases |",
	"",
	"The existing methods are unchanged. This is demo data: no files were modified and no commands were executed.",
	"",
	"---",
	"",
	"### Markdown check",
	"",
	"The rest of this reply shows every kind of Markdown the chat renders.",
	"",
	"Text can be *emphasized*, **strong**, ***both***, ~~struck~~, or `code`. Links come as [text](https://go.dev), autolinks as <https://pkg.go.dev/errors>, www.example.com, and gopher@example.com, and inline HTML as written: <kbd>Enter</kbd>. Escapes show their character, as in \\*not emphasis\\*, and entities too: &copy; &amp; &#x2192;.",
	"A line that ends with two spaces  ",
	"breaks here, and one that ends with a backslash\\",
	"breaks too.",
	"",
	"#### A smaller heading",
	"",
	"A setext heading",
	"----------------",
	"",
	"> A quote can hold paragraphs,",
	">",
	"> > other quotes,",
	">",
	"> - and lists.",
	"",
	"1. An ordered list",
	"2. with a nested list:",
	"   - a bullet",
	"   - and another",
	"3. and a third item.",
	"",
	"* A loose list",
	"",
	"* has blank lines between its items.",
	"",
	"- [x] A finished task",
	"- [ ] An open task",
	"",
	"| Left | Center | Right |",
	"|:-----|:------:|------:|",
	"| a | b | c |",
	"| `Divide` | **3.5** | 7 / 2 |",
	"",
	"```go",
	"func (c *Calculator) Divide(a, b float64) (float64, error) {",
	"\tif b == 0 {",
	"\t\treturn 0, errors.New(\"division by zero\")",
	"\t}",
	"\treturn a / b, nil",
	"}",
	"```",
	"",
	"    An indented code block.",
	"",
	"Wide characters join across line breaks: 割り算の",
	"結果を返します。 Escaped spaces show nothing: **“強調”**\\ です。",
	"",
	"![An image shows its description](https://go.dev/images/go-logo-blue.svg)",
	"",
	"<details>",
	"<summary>Raw HTML shows as text</summary>",
	"</details>",
}, "\n")

const calculatorSource = `package calculator

// Calculator provides stateless arithmetic operations.
type Calculator struct{}

// Add returns the sum of a and b.
func (c *Calculator) Add(a, b float64) float64 {
	return a + b
}

// Subtract returns the difference of a and b.
func (c *Calculator) Subtract(a, b float64) float64 {
	return a - b
}

// Multiply returns the product of a and b.
func (c *Calculator) Multiply(a, b float64) float64 {
	return a * b
}
`

// finalSource is calc.go once the demo's edits have added the import and
// Divide.
const finalSource = `package calculator

import "errors"

// Calculator provides stateless arithmetic operations.
type Calculator struct{}

// Divide returns the quotient, or an error if b is zero.
func (c *Calculator) Divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a / b, nil
}

// Add returns the sum of a and b.
func (c *Calculator) Add(a, b float64) float64 {
	return a + b
}

// Subtract returns the difference of a and b.
func (c *Calculator) Subtract(a, b float64) float64 {
	return a - b
}

// Multiply returns the product of a and b.
func (c *Calculator) Multiply(a, b float64) float64 {
	return a * b
}
`

// divideSource is calc.go's Divide method as read back after the edits,
// lines 8 to 14 of 29, as finalSource holds them.
const divideSource = `// Divide returns the quotient, or an error if b is zero.
func (c *Calculator) Divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a / b, nil
}

[showing lines 8-14 of 29]`

const calculatorTests = `package calculator

import "testing"

func TestAdd(t *testing.T) {
	c := &Calculator{}
	if got := c.Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %v, want 5", got)
	}
}
`

// divideRun is the output of the first divide test run, which fails.
const divideRun = `=== RUN   TestDivide
    divide_test.go:9: Divide(7, 2) = 3.5, <nil>; want 3, nil
--- FAIL: TestDivide (0.00s)
=== RUN   TestDivideByZero
--- PASS: TestDivideByZero (0.00s)
FAIL
FAIL	example/calculator	0.004s
FAIL
`

// benchRun is the output of the benchmark before it times out.
const benchRun = `goos: darwin
goarch: arm64
pkg: example/calculator
cpu: Apple M3
`

// watchRun is the output of the test watcher before it is cancelled.
const watchRun = `ok  	example/calculator	0.003s
`
