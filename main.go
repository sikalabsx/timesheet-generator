package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"gopkg.in/yaml.v3"
)

const (
	dateLayout     = "2006-01-02"
	dateTimeLayout = "2006-01-02 15:04"
)

type Timesheet struct {
	Meta struct {
		Version int `yaml:"version"`
	} `yaml:"meta"`
	Client struct {
		Name string `yaml:"name"`
	} `yaml:"client"`
	Header            string     `yaml:"header"`
	Footer            string     `yaml:"footer"`
	HideSummaryByTask bool       `yaml:"hide_summary_by_task"`
	Entries           []RawEntry `yaml:"entries"`
}

// textLines splits multi-line YAML text into lines, dropping trailing empty ones.
func textLines(s string) []string {
	s = strings.TrimRight(s, "\n ")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

type RawEntry struct {
	Date     string `yaml:"date"`
	Duration string `yaml:"duration"`
	From     string `yaml:"from"`
	To       string `yaml:"to"`
	TaskName string `yaml:"task_name"`
	TaskID   string `yaml:"task_id"`
}

type Entry struct {
	Date     time.Time
	From     *time.Time
	To       *time.Time
	Duration time.Duration
	TaskName string
	TaskID   string
}

func main() {
	file := flag.String("f", "timesheet.local.yaml", "timesheet YAML file")
	outDir := flag.String("o", "out", "output directory for PDFs")
	month := flag.String("m", "", "generate only this month (YYYY-MM)")
	flag.Parse()

	ts, err := load(*file)
	if err != nil {
		log.Fatal(err)
	}

	entries, err := parseEntries(ts.Entries)
	if err != nil {
		log.Fatal(err)
	}

	byMonth := map[string][]Entry{}
	for _, e := range entries {
		key := e.Date.Format("2006-01")
		byMonth[key] = append(byMonth[key], e)
	}

	if *month != "" {
		if _, ok := byMonth[*month]; !ok {
			log.Fatalf("no entries for month %s", *month)
		}
		byMonth = map[string][]Entry{*month: byMonth[*month]}
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	months := make([]string, 0, len(byMonth))
	for m := range byMonth {
		months = append(months, m)
	}
	sort.Strings(months)

	prefix := filepath.Join(*outDir, "timesheet-"+slug(ts.Client.Name)+"-")
	for _, m := range months {
		es := byMonth[m]
		sort.SliceStable(es, func(i, j int) bool { return es[i].sortKey().Before(es[j].sortKey()) })
		path := prefix + m + ".pdf"
		if err := renderMonth(path, ts, es[0].Date, es); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s  %d entries  %s\n", path, len(es), fmtDuration(total(es)))
	}

	// Yearly summaries (skipped when generating a single month).
	if *month != "" {
		return
	}
	monthsByYear := map[string][]string{}
	var years []string
	for _, m := range months {
		y := m[:4]
		if monthsByYear[y] == nil {
			years = append(years, y)
		}
		monthsByYear[y] = append(monthsByYear[y], m)
	}
	for _, y := range years {
		var t time.Duration
		for _, m := range monthsByYear[y] {
			t += total(byMonth[m])
		}
		path := prefix + y + "-summary.pdf"
		if err := renderYear(path, ts, y, monthsByYear[y], byMonth); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s  %d months  %s\n", path, len(monthsByYear[y]), fmtDuration(t))
		path = prefix + y + "-all.pdf"
		if err := renderYearAll(path, ts, y, monthsByYear[y], byMonth); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s  %d months  %s\n", path, len(monthsByYear[y]), fmtDuration(t))
	}
}

func load(path string) (*Timesheet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ts Timesheet
	if err := yaml.Unmarshal(data, &ts); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if ts.Meta.Version != 1 {
		return nil, fmt.Errorf("unsupported meta.version %d", ts.Meta.Version)
	}
	return &ts, nil
}

// parseFromTo parses "YYYY-MM-DD HH:MM", or "HH:MM" when the entry has a date.
func parseFromTo(date, s string) (time.Time, error) {
	if date == "" {
		return time.ParseInLocation(dateTimeLayout, s, time.Local)
	}
	if t, err := time.ParseInLocation(dateTimeLayout, s, time.Local); err == nil {
		if t.Format(dateLayout) != date {
			return time.Time{}, fmt.Errorf("%q is not on date %s", s, date)
		}
		return t, nil
	}
	t, err := time.ParseInLocation(dateLayout+" 15:04", date+" "+s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected HH:MM, got %q", s)
	}
	return t, nil
}

func isTimeOnly(s string) bool {
	_, err := time.Parse("15:04", s)
	return err == nil
}

func parseEntries(raw []RawEntry) ([]Entry, error) {
	var out []Entry
	for i, r := range raw {
		e := Entry{TaskName: r.TaskName, TaskID: r.TaskID}
		errf := func(format string, a ...any) error {
			label := strings.TrimSpace(strings.Join([]string{r.TaskID, r.TaskName}, " "))
			return fmt.Errorf("entry #%d (%s): %s", i+1, label, fmt.Sprintf(format, a...))
		}
		switch {
		case r.From != "" || r.To != "":
			if r.From == "" || r.To == "" {
				return nil, errf("both from and to are required")
			}
			if r.Duration != "" {
				return nil, errf("use either from/to or duration, not both")
			}
			from, err := parseFromTo(r.Date, r.From)
			if err != nil {
				return nil, errf("invalid from: %v", err)
			}
			to, err := parseFromTo(r.Date, r.To)
			if err != nil {
				return nil, errf("invalid to: %v", err)
			}
			// Time-only "to" earlier than "from" means it ends the next day (e.g. 22:20 – 00:00).
			if r.Date != "" && isTimeOnly(r.To) && to.Before(from) {
				to = to.AddDate(0, 0, 1)
			}
			if !to.After(from) {
				return nil, errf("to must be after from")
			}
			e.From, e.To = &from, &to
			e.Date = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local)
			e.Duration = to.Sub(from)
		case r.Date != "":
			if r.Duration == "" {
				return nil, errf("duration is required with date")
			}
			d, err := time.ParseInLocation(dateLayout, r.Date, time.Local)
			if err != nil {
				return nil, errf("invalid date: %v", err)
			}
			dur, err := time.ParseDuration(r.Duration)
			if err != nil || dur <= 0 {
				return nil, errf("invalid duration %q", r.Duration)
			}
			e.Date, e.Duration = d, dur
		default:
			return nil, errf("either date/duration or from/to is required")
		}
		out = append(out, e)
	}
	return out, nil
}

func (e Entry) sortKey() time.Time {
	if e.From != nil {
		return *e.From
	}
	return e.Date
}

func total(es []Entry) time.Duration {
	var t time.Duration
	for _, e := range es {
		t += e.Duration
	}
	return t
}

// fmtHours formats d as decimal hours, e.g. 1h30m -> "1.5h", 10h35m -> "10.58h".
func fmtHours(d time.Duration) string {
	h := d.Round(time.Minute).Hours()
	return strconv.FormatFloat(math.Round(h*100)/100, 'f', -1, 64) + "h"
}

// fmtMD formats d as man-days (8h), e.g. 12h -> "1.5 MD".
func fmtMD(d time.Duration) string {
	md := d.Round(time.Minute).Hours() / 8
	return strconv.FormatFloat(math.Round(md*100)/100, 'f', -1, 64) + " MD"
}

// fmtTotal formats d as "H:MM (decimal h, man-days)", e.g. "12:00 (12h, 1.5 MD)".
func fmtTotal(d time.Duration) string {
	return fmtDuration(d) + " (" + fmtHours(d) + ", " + fmtMD(d) + ")"
}

func fmtDuration(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	return fmt.Sprintf("%d:%02d", m/60, m%60)
}

func slug(s string) string {
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

// newDoc creates an A4 document with fonts, Markdown header/footer and page numbers.
// It returns the document and its bottom margin (which includes the footer).
func newDoc(ts *Timesheet) (*fpdf.Fpdf, float64) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	pdf.AddUTF8FontFromBytes("Go", "B", gobold.TTF)
	pdf.AddUTF8FontFromBytes("Go", "I", goitalic.TTF)
	pdf.AddUTF8FontFromBytes("Go", "BI", gobolditalic.TTF)
	pdf.AddUTF8FontFromBytes("GoMono", "", gomono.TTF)
	pdf.SetMargins(15, 15, 15)

	const (
		headerSize  = 11.0
		footerSize  = 8.0
		lineSpacing = 1.5
	)
	headerLines := parseMarkdown(ts.Header)
	footerLines := parseMarkdown(ts.Footer)
	// The page number sits on the last footer line (or alone if there is no footer).
	pageNoLine := mdLine{scale: 1}
	if len(footerLines) > 0 {
		pageNoLine = footerLines[len(footerLines)-1]
	}
	pageNoH := pageNoLine.height(footerSize, lineSpacing)
	footerH := max(markdownHeight(footerLines, footerSize, lineSpacing), pageNoH)
	bottomMargin := 15 + footerH
	pdf.SetAutoPageBreak(true, bottomMargin)

	pdf.SetHeaderFunc(func() {
		if len(headerLines) == 0 {
			return
		}
		renderMarkdown(pdf, headerLines, headerSize, lineSpacing)
		pdf.Ln(1)
	})
	pdf.SetFooterFunc(func() {
		pdf.SetY(-(10 + footerH))
		renderMarkdown(pdf, footerLines, footerSize, lineSpacing)
		pdf.SetY(-(10 + pageNoH))
		pdf.SetFont("Go", "", footerSize)
		pdf.CellFormat(0, pageNoH, fmt.Sprintf("Page %d/{nb}", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AliasNbPages("")
	pdf.AddPage()
	return pdf, bottomMargin
}

func renderTitle(pdf *fpdf.Fpdf, title string) {
	pdf.SetFont("Go", "B", 18)
	pdf.CellFormat(0, 10, title, "", 1, "L", false, 0, "")
	pdf.Ln(4)
}

type column struct {
	title string
	w     float64
	align string
}

// renderTable draws a table, repeating the column header after page breaks,
// followed by a bold total row in the last column.
func renderTable(pdf *fpdf.Fpdf, bottomMargin float64, cols []column, rows [][]string, totalLabel, totalValue string) {
	header := func() {
		pdf.SetFont("Go", "B", 10)
		pdf.SetFillColor(230, 230, 230)
		for _, c := range cols {
			pdf.CellFormat(c.w, 8, c.title, "B", 0, c.align, true, 0, "")
		}
		pdf.Ln(-1)
		pdf.SetFont("Go", "", 10)
	}
	header()
	for _, row := range rows {
		if _, pageH := pdf.GetPageSize(); pdf.GetY()+7 > pageH-bottomMargin {
			pdf.AddPage()
			header()
		}
		for i, c := range cols {
			pdf.CellFormat(c.w, 7, fit(pdf, row[i], c.w-2), "B", 0, c.align, false, 0, "")
		}
		pdf.Ln(-1)
	}
	if totalLabel == "" {
		return
	}
	var tableW float64
	for _, c := range cols {
		tableW += c.w
	}
	pdf.SetFont("Go", "B", 10)
	// The value may be wider than the last column (e.g. "10:35 (10.58h)").
	valueW := max(cols[len(cols)-1].w, pdf.GetStringWidth(totalValue)+2)
	pdf.CellFormat(tableW-valueW, 8, totalLabel, "", 0, "R", false, 0, "")
	pdf.CellFormat(valueW, 8, totalValue, "", 1, "R", false, 0, "")
}

func renderMonth(path string, ts *Timesheet, month time.Time, es []Entry) error {
	pdf, bottomMargin := newDoc(ts)
	renderTitle(pdf, "Timesheet — "+ts.Client.Name+" — "+month.Format("01/2006"))
	renderEntries(pdf, bottomMargin, es)
	if !ts.HideSummaryByTask {
		renderSummary(pdf, es, bottomMargin)
	}
	return pdf.OutputFileAndClose(path)
}

// renderEntries draws the table of entries with a total row.
func renderEntries(pdf *fpdf.Fpdf, bottomMargin float64, es []Entry) {
	var rows [][]string
	for _, e := range es {
		timeRange := ""
		if e.From != nil {
			timeRange = e.From.Format("15:04") + " – " + e.To.Format("15:04")
		}
		rows = append(rows, []string{e.Date.Format("02.01.2006"), timeRange, e.TaskID, e.TaskName, fmtDuration(e.Duration)})
	}
	renderTable(pdf, bottomMargin, []column{
		{"Date", 25, "L"},
		{"Time", 28, "L"},
		{"Task ID", 25, "L"},
		{"Task", 82, "L"},
		{"Hours", 20, "R"},
	}, rows, "Total", fmtTotal(total(es)))
}

// renderHeading draws a section heading, starting a new page if the heading,
// the table header and the first row would not fit.
func renderHeading(pdf *fpdf.Fpdf, bottomMargin float64, text string, size float64) {
	if _, pageH := pdf.GetPageSize(); pdf.GetY()+8+8+8+7 > pageH-bottomMargin {
		pdf.AddPage()
	} else {
		pdf.Ln(8)
	}
	pdf.SetFont("Go", "B", size)
	pdf.CellFormat(0, 8, text, "", 1, "L", false, 0, "")
}

// renderYearAll renders all entries of the year, one H2 section per month.
func renderYearAll(path string, ts *Timesheet, year string, months []string, byMonth map[string][]Entry) error {
	pdf, bottomMargin := newDoc(ts)
	renderTitle(pdf, "Timesheet — "+ts.Client.Name+" — "+year)

	var all []Entry
	for _, m := range months {
		all = append(all, byMonth[m]...)
	}
	pdf.SetFont("Go", "B", 12)
	pdf.CellFormat(0, 8, "Total: "+fmtTotal(total(all)), "", 1, "L", false, 0, "")
	pdf.Ln(4)

	for i, m := range months {
		es := byMonth[m]
		if i == 0 {
			pdf.SetFont("Go", "B", 14)
			pdf.CellFormat(0, 8, es[0].Date.Format("01/2006"), "", 1, "L", false, 0, "")
		} else {
			renderHeading(pdf, bottomMargin, es[0].Date.Format("01/2006"), 14)
		}
		renderEntries(pdf, bottomMargin, es)
	}

	if !ts.HideSummaryByTask {
		renderSummary(pdf, all, bottomMargin)
	}
	return pdf.OutputFileAndClose(path)
}

func renderYear(path string, ts *Timesheet, year string, months []string, byMonth map[string][]Entry) error {
	pdf, bottomMargin := newDoc(ts)
	renderTitle(pdf, "Yearly Summary — "+ts.Client.Name+" — "+year)

	var rows [][]string
	var all []Entry
	for _, m := range months {
		es := byMonth[m]
		all = append(all, es...)
		rows = append(rows, []string{es[0].Date.Format("01/2006"), fmt.Sprint(len(es)), fmtDuration(total(es))})
	}
	renderTable(pdf, bottomMargin, []column{
		{"Month", 50, "L"},
		{"Entries", 110, "R"},
		{"Hours", 20, "R"},
	}, rows, "Total", fmtTotal(total(all)))

	if !ts.HideSummaryByTask {
		renderSummary(pdf, all, bottomMargin)
	}
	return pdf.OutputFileAndClose(path)
}

func renderSummary(pdf *fpdf.Fpdf, es []Entry, bottomMargin float64) {
	type taskSum struct {
		id, name string
		d        time.Duration
	}
	var sums []*taskSum
	idx := map[string]*taskSum{}
	for _, e := range es {
		k := e.TaskID + "\x00" + e.TaskName
		if idx[k] == nil {
			idx[k] = &taskSum{id: e.TaskID, name: e.TaskName}
			sums = append(sums, idx[k])
		}
		idx[k].d += e.Duration
	}
	renderHeading(pdf, bottomMargin, "Summary by task", 12)
	var rows [][]string
	for _, s := range sums {
		rows = append(rows, []string{s.id, s.name, fmtDuration(s.d)})
	}
	renderTable(pdf, bottomMargin, []column{
		{"Task ID", 25, "L"},
		{"Task", 135, "L"},
		{"Hours", 20, "R"},
	}, rows, "", "")
}

// fit truncates s with an ellipsis so it fits into width w.
func fit(pdf *fpdf.Fpdf, s string, w float64) string {
	if pdf.GetStringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && pdf.GetStringWidth(string(r)+"…") > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
