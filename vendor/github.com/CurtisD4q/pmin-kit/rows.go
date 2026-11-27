package mint

import (
	"fmt"
	"math"
	"strconv"
)

// Style sets the visual emphasis of a Label row.
type Style int

// TextStyle is an alias for Style.
type TextStyle = Style

const (
	StyleBody Style = iota
	StyleTitle
	StyleHeading
	StyleCaption
	StyleItalic
	StyleUnderline
)

// Context is an alias for Ctx so callbacks may refer to either name.
type Context = Ctx

// widgetType says which kind of row a Row is. The values are the strings the
// code has always used; naming them lets the compiler catch a misspelling.
type widgetType string

const (
	wtValue       widgetType = "value"
	wtAction      widgetType = "action"
	wtToggle      widgetType = "toggle"
	wtCheck       widgetType = "check"
	wtRadioGroup  widgetType = "radiogroup" // expanded into one wtRadio per option
	wtRadio       widgetType = "radio"
	wtPicker      widgetType = "picker"
	wtSlider      widgetType = "slider"
	wtProgress    widgetType = "progress"
	wtStepper     widgetType = "stepper"
	wtText        widgetType = "text"
	wtNote        widgetType = "note"
	wtLabel       widgetType = "label"
	wtDivider     widgetType = "divider"
	wtSpacer      widgetType = "spacer"
	wtLog         widgetType = "log"
	wtLogLine     widgetType = "logline" // one line of a log, made when the log is expanded
	wtCollapsible widgetType = "collapsible"
	wtTable       widgetType = "table"
	wtTableRow    widgetType = "tablerow" // one row of a table, made when the table is expanded
	wtCard        widgetType = "card"     // a details card, expanded into its lines
)

// Row is one item in a Section. Construct rows with Value, Action, Toggle,
// Checkbox, Radio, Picker (or Select), Slider, Progress, Stepper, Text,
// Note, Label, Heading, Divider, Spacer, Log, Collapsible, or Table.
type Row struct {
	kind        string // "row" or "section"
	name        string
	collapsible bool
	collapsed   bool
	hidden      int
	openPtr     *bool
	badge       string

	wtype       widgetType
	id          string
	label       string
	value       string
	hasValue    bool
	hint        string
	placeholder string
	unit        string
	divChar     string
	radioOpt    string
	indent      int
	style       Style

	min, max, stepBy int
	hasBounds        bool
	snap             bool

	options     []string
	optionsFunc func() []string

	getStr     func() string
	setStr     func(string)
	getBool    func() bool
	setBool    func(bool)
	getInt     func() int
	setInt     func(int)
	step       func(int)
	format     func(int) string
	onActivate func(*Ctx)

	logHeight int
	logLines  func() []LogLine
	logLine   LogLine
	logEmpty  bool

	children  []Row
	textLines func() []string
	textLog   func() []LogLine // as textLines, with the program's colours
	chunkSize int

	headers     []string
	headersFn   func() []string
	tableRows   [][]string
	tableGet    func() [][]string
	tableSelect func(*Ctx, int, []string)
	separator   string

	// viewer keeps a table's columns at their natural width and scrolls the
	// whole table sideways, instead of shrinking the widest column to fit.
	viewer     bool
	hscroll    int         // columns scrolled off the left, in cells
	scrollRows int         // rows scrolled off the top, when the body is taller
	colWidths  map[int]int // column -> width, when the reader has resized it
	// pin marks the header and its rule so the screen holds them in place
	// while the body scrolls.
	pin bool
	// table is the table row a tablerow came from, so ← → can scroll it and
	// H L resize its columns. Nil for everything else.
	table *Row

	// alert paints the whole row red and bold. The label text carries the
	// same warning, so it still reads when the terminal has no colour.
	alert bool

	// cells is the raw column values of a tablerow, with cellHead the
	// headers and cellIdx the row index in the table. A split detail pane
	// reads them as the cursor moves.
	cells    []string
	cellHead []string
	cellIdx  int

	// A styled table: its column styles and mark function (on the table
	// row), and on each of its rows the cells as drawn, the row's mark and
	// the margin kept for marks.
	columns []Column
	markFn  func(int, []string) Mark
	segs    []cellSeg
	mark    Mark
	gutter  int

	// card holds a details card before it is expanded; cardBody marks the
	// lines of its body, which scroll under its pinned heading.
	card     *CardOpts
	cardBody bool

	// tableHead marks a table's headings and the rule under them, which a
	// filter keeps.
	tableHead bool

	// does says what Enter does on the row (see Does).
	does func() string
}

// TableCells returns the column values of a table row. ok is false for
// every other kind of row.
func (r Row) TableCells() (headers, cells []string, index int, ok bool) {
	if r.wtype != wtTableRow {
		return nil, nil, 0, false
	}
	return append([]string(nil), r.cellHead...), append([]string(nil), r.cells...), r.cellIdx, true
}

func (r Row) normalized() Row {
	if r.kind == "" {
		r.kind = "row"
	}
	if r.wtype == "" {
		r.wtype = wtValue
	}
	if r.wtype == wtSlider {
		if r.min == 0 && r.max == 0 {
			r.min, r.max = 0, 100
		}
		if r.stepBy <= 0 {
			r.stepBy = 5
		}
		r.hasBounds = true
	}
	if r.wtype == wtStepper {
		if r.stepBy <= 0 {
			r.stepBy = 1
		}
		if r.min != 0 || r.max != 0 {
			r.hasBounds = true
		}
	}
	if r.wtype == wtDivider && r.divChar == "" {
		r.divChar = "─"
	}
	if r.wtype == wtLog && r.logHeight <= 0 {
		r.logHeight = 6
	}
	return r
}

func (r *Row) isSelectable() bool {
	if r.kind == "section" {
		return r.collapsible
	}
	switch r.wtype {
	case wtNote, wtDivider, wtSpacer, wtLabel, wtLogLine, wtLog, wtProgress:
		return false
	}
	return true
}

func (r *Row) hasGet() bool {
	return r.getStr != nil || r.getBool != nil || r.getInt != nil
}

func (r *Row) callGet() (val any, msg string, failed bool) {
	defer func() {
		if p := recover(); p != nil {
			val = nil
			msg = CleanLine(fmt.Sprint(p))
			failed = true
		}
	}()
	switch {
	case r.getStr != nil:
		return r.getStr(), "", false
	case r.getBool != nil:
		return r.getBool(), "", false
	case r.getInt != nil:
		return r.getInt(), "", false
	}
	return nil, "", false
}

func (r *Row) numberText(v int) string {
	if r.format != nil {
		var out string
		func() {
			defer func() {
				if p := recover(); p != nil {
					ToastError(fmt.Sprint(p))
					out = strconv.Itoa(v)
				}
			}()
			out = r.format(v)
		}()
		return out
	}
	if r.unit != "" {
		return fmt.Sprintf("%d %s", v, r.unit)
	}
	return strconv.Itoa(v)
}

func (r *Row) limited() bool {
	return r.hasBounds
}

func (r *Row) resolveOptions() []string {
	if r.optionsFunc != nil {
		var out []string
		func() {
			defer func() {
				if p := recover(); p != nil {
					ToastError(fmt.Sprint(p))
				}
			}()
			out = r.optionsFunc()
		}()
		return out
	}
	return r.options
}

func protect(fn func()) {
	defer func() {
		if p := recover(); p != nil {
			ToastError(fmt.Sprint(p))
		}
	}()
	fn()
}

func tostr(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(x)
	}
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int:
		return x != 0
	case string:
		return x != "" && x != "false" && x != "0"
	}
	return false
}

func toNumber(v any) float64 {
	switch x := v.(type) {
	case int:
		return finiteNumber(float64(x))
	case int8:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return finiteNumber(float64(x))
	case int64:
		return finiteNumber(float64(x))
	case uint:
		return finiteNumber(float64(x))
	case uint8:
		return float64(x)
	case uint16:
		return float64(x)
	case uint32:
		return finiteNumber(float64(x))
	case uint64:
		return finiteNumber(float64(x))
	case float32:
		return finiteNumber(float64(x))
	case float64:
		return finiteNumber(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return finiteNumber(f)
	}
	return 0
}

// maxExactNumber is the largest whole number a float64 holds exactly. A
// value stored as text ("1e400", "NaN") is kept within it, so stepping it
// never turns into a nonsense int (converting infinity or NaN to an int is
// undefined).
const maxExactNumber = 1 << 53

func finiteNumber(f float64) float64 {
	switch {
	case math.IsNaN(f):
		return 0
	case f > maxExactNumber:
		return maxExactNumber
	case f < -maxExactNumber:
		return -maxExactNumber
	}
	return f
}

// ── Row constructors ─────────────────────────────────────────────────────────

// ValueOpts configures a read-only (or drill-down) key/value row.
type ValueOpts struct {
	ID         string
	Label      string
	Value      string
	Get        func() string
	OnActivate func(*Ctx)
}

// Value creates a row displaying a label on the left and a value on the right.
func Value(opts ValueOpts) Row {
	return Row{
		kind:       "row",
		wtype:      wtValue,
		id:         opts.ID,
		label:      opts.Label,
		value:      opts.Value,
		hasValue:   opts.Value != "",
		getStr:     opts.Get,
		onActivate: opts.OnActivate,
	}
}

// ActionOpts configures a clickable action row.
type ActionOpts struct {
	ID         string
	Label      string
	Hint       string
	Alert      bool
	Get        func() string
	OnActivate func(*Ctx)
	// Does, optional, says what Enter does, in a few words ("runs nmap
	// -sV 10.0.0.5"). Without it the label is shown.
	Does func() string
}

// Action creates an interactive command/navigation row.
func Action(opts ActionOpts) Row {
	return Row{
		kind:       "row",
		wtype:      wtAction,
		id:         opts.ID,
		label:      opts.Label,
		hint:       opts.Hint,
		alert:      opts.Alert,
		getStr:     opts.Get,
		onActivate: opts.OnActivate,
		does:       opts.Does,
	}
}

// ToggleOpts configures an immediate ON/OFF switch row.
type ToggleOpts struct {
	ID    string
	Label string
	Get   func() bool
	Set   func(bool)
}

// Toggle creates an ON/OFF toggle row.
func Toggle(opts ToggleOpts) Row {
	return Row{
		kind:    "row",
		wtype:   wtToggle,
		id:      opts.ID,
		label:   opts.Label,
		getBool: opts.Get,
		setBool: opts.Set,
	}
}

// CheckboxOpts configures a checkbox row (☐ / ☑).
type CheckboxOpts struct {
	ID    string
	Label string
	Get   func() bool
	Set   func(bool)
}

// Checkbox creates a checkbox row for boolean flags.
func Checkbox(opts CheckboxOpts) Row {
	return Row{
		kind:    "row",
		wtype:   wtCheck,
		id:      opts.ID,
		label:   opts.Label,
		getBool: opts.Get,
		setBool: opts.Set,
	}
}

// RadioOpts configures a group of mutually exclusive radio options (○ / ◉).
type RadioOpts struct {
	ID      string
	Label   string
	Options []string
	Get     func() string
	Set     func(string)
}

// Radio creates a radio-button group row.
func Radio(opts RadioOpts) Row {
	return Row{
		kind:    "row",
		wtype:   wtRadioGroup,
		id:      opts.ID,
		label:   opts.Label,
		options: opts.Options,
		getStr:  opts.Get,
		setStr:  opts.Set,
	}
}

// PickerOpts configures a left/right cycling picker row (‹ value ›).
type PickerOpts struct {
	ID          string
	Label       string
	Options     []string
	OptionsFunc func() []string
	Get         func() string
	Set         func(string)
	Step        func(int)
}

// SelectOpts is an alias for PickerOpts.
type SelectOpts = PickerOpts

// Picker creates a cycling choice row.
func Picker(opts PickerOpts) Row {
	return Row{
		kind:        "row",
		wtype:       wtPicker,
		id:          opts.ID,
		label:       opts.Label,
		options:     opts.Options,
		optionsFunc: opts.OptionsFunc,
		getStr:      opts.Get,
		setStr:      opts.Set,
		step:        opts.Step,
	}
}

// Select is an alias for Picker.
func Select(opts SelectOpts) Row { return Picker(opts) }

// SliderOpts configures a horizontal bar slider row.
type SliderOpts struct {
	ID     string
	Label  string
	Min    int
	Max    int
	Step   int
	Unit   string
	Snap   bool
	Format func(int) string
	Get    func() int
	Set    func(int)
}

// Slider creates a slider row.
func Slider(opts SliderOpts) Row {
	minV, maxV := opts.Min, opts.Max
	if minV == 0 && maxV == 0 {
		maxV = 100
	}
	step := opts.Step
	if step <= 0 {
		step = 5
	}
	return Row{
		kind:      "row",
		wtype:     wtSlider,
		id:        opts.ID,
		label:     opts.Label,
		min:       minV,
		max:       maxV,
		stepBy:    step,
		hasBounds: true,
		unit:      opts.Unit,
		snap:      opts.Snap || opts.Step > 0,
		format:    opts.Format,
		getInt:    opts.Get,
		setInt:    opts.Set,
	}
}

// ProgressOpts configures a read-only percentage progress bar row.
type ProgressOpts struct {
	ID     string
	Label  string
	Format func(int) string
	Get    func() int
}

// Progress creates a progress bar row (0–100%).
func Progress(opts ProgressOpts) Row {
	return Row{
		kind:   "row",
		wtype:  wtProgress,
		id:     opts.ID,
		label:  opts.Label,
		min:    0,
		max:    100,
		format: opts.Format,
		getInt: opts.Get,
	}
}

// StepperOpts configures a numeric − / + stepper row.
type StepperOpts struct {
	ID     string
	Label  string
	Min    int
	Max    int
	Step   int
	Unit   string
	Format func(int) string
	Get    func() int
	Set    func(int)
}

// Stepper creates a numeric stepper row.
func Stepper(opts StepperOpts) Row {
	step := opts.Step
	if step <= 0 {
		step = 1
	}
	return Row{
		kind:      "row",
		wtype:     wtStepper,
		id:        opts.ID,
		label:     opts.Label,
		min:       opts.Min,
		max:       opts.Max,
		stepBy:    step,
		hasBounds: opts.Min != 0 || opts.Max != 0,
		unit:      opts.Unit,
		format:    opts.Format,
		getInt:    opts.Get,
		setInt:    opts.Set,
	}
}

// TextOpts configures an inline-editable text field row.
type TextOpts struct {
	ID          string
	Label       string
	Placeholder string
	Value       string
	Alert       bool
	Get         func() string
	Set         func(string)
}

// Text creates an inline-editable text row.
func Text(opts TextOpts) Row {
	return Row{
		kind:        "row",
		wtype:       wtText,
		id:          opts.ID,
		label:       opts.Label,
		placeholder: opts.Placeholder,
		value:       opts.Value,
		hasValue:    opts.Value != "",
		alert:       opts.Alert,
		getStr:      opts.Get,
		setStr:      opts.Set,
	}
}

// Note creates a single-line dim note row.
func Note(text string) Row {
	return Row{kind: "row", wtype: wtNote, label: text}
}

// Label creates a styled text row that wraps across the screen width.
func Label(style Style, text string) Row {
	return Row{kind: "row", wtype: wtLabel, style: style, label: text}
}

// Heading creates a bold sub-heading row.
func Heading(text string) Row {
	return Label(StyleHeading, text)
}

// Divider creates a horizontal rule row using the optional character (default "─").
func Divider(char ...string) Row {
	ch := "─"
	if len(char) > 0 && char[0] != "" {
		ch = char[0]
	}
	return Row{kind: "row", wtype: wtDivider, divChar: ch}
}

// Spacer creates a blank line row.
func Spacer() Row {
	return Row{kind: "row", wtype: wtSpacer}
}
