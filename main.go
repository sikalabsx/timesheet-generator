package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
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
	Entries []RawEntry `yaml:"entries"`
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

	for _, m := range months {
		es := byMonth[m]
		sort.SliceStable(es, func(i, j int) bool { return es[i].sortKey().Before(es[j].sortKey()) })
		path := filepath.Join(*outDir, fmt.Sprintf("timesheet-%s-%s.pdf", slug(ts.Client.Name), m))
		if err := render(path, ts.Client.Name, es[0].Date, es); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s  %d entries  %s\n", path, len(es), fmtDuration(total(es)))
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

func parseEntries(raw []RawEntry) ([]Entry, error) {
	var out []Entry
	for i, r := range raw {
		e := Entry{TaskName: r.TaskName, TaskID: r.TaskID}
		errf := func(format string, a ...any) error {
			return fmt.Errorf("entry #%d (%s): %s", i+1, r.TaskID, fmt.Sprintf(format, a...))
		}
		switch {
		case r.From != "" || r.To != "":
			if r.From == "" || r.To == "" {
				return nil, errf("both from and to are required")
			}
			if r.Date != "" || r.Duration != "" {
				return nil, errf("use either from/to or date/duration, not both")
			}
			from, err := time.ParseInLocation(dateTimeLayout, r.From, time.Local)
			if err != nil {
				return nil, errf("invalid from: %v", err)
			}
			to, err := time.ParseInLocation(dateTimeLayout, r.To, time.Local)
			if err != nil {
				return nil, errf("invalid to: %v", err)
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

func fmtDuration(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	return fmt.Sprintf("%d:%02d", m/60, m%60)
}

func slug(s string) string {
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

func render(path, client string, month time.Time, es []Entry) error {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	pdf.AddUTF8FontFromBytes("Go", "B", gobold.TTF)
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 15)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Go", "", 8)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(0, 5, fmt.Sprintf("Page %d/{nb}", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AliasNbPages("")
	pdf.AddPage()

	// Header
	pdf.SetFont("Go", "B", 18)
	pdf.CellFormat(0, 10, "Timesheet", "", 1, "L", false, 0, "")
	pdf.SetFont("Go", "", 11)
	pdf.CellFormat(25, 6, "Client:", "", 0, "L", false, 0, "")
	pdf.SetFont("Go", "B", 11)
	pdf.CellFormat(0, 6, client, "", 1, "L", false, 0, "")
	pdf.SetFont("Go", "", 11)
	pdf.CellFormat(25, 6, "Period:", "", 0, "L", false, 0, "")
	pdf.SetFont("Go", "B", 11)
	pdf.CellFormat(0, 6, month.Format("January 2006"), "", 1, "L", false, 0, "")
	pdf.Ln(6)

	// Entries table
	cols := []struct {
		title string
		w     float64
		align string
	}{
		{"Date", 25, "L"},
		{"Time", 28, "L"},
		{"Task ID", 25, "L"},
		{"Task", 82, "L"},
		{"Hours", 20, "R"},
	}
	header := func() {
		pdf.SetFont("Go", "B", 10)
		pdf.SetFillColor(230, 230, 230)
		for _, c := range cols {
			pdf.CellFormat(c.w, 8, c.title, "B", 0, c.align, true, 0, "")
		}
		pdf.Ln(-1)
	}
	header()
	pdf.SetFont("Go", "", 10)
	for _, e := range es {
		if pdf.GetY() > 270 {
			pdf.AddPage()
			header()
			pdf.SetFont("Go", "", 10)
		}
		timeRange := ""
		if e.From != nil {
			timeRange = e.From.Format("15:04") + " – " + e.To.Format("15:04")
		}
		vals := []string{e.Date.Format("Mon 02.01."), timeRange, e.TaskID, e.TaskName, fmtDuration(e.Duration)}
		for i, c := range cols {
			pdf.CellFormat(c.w, 7, fit(pdf, vals[i], c.w-2), "B", 0, c.align, false, 0, "")
		}
		pdf.Ln(-1)
	}
	pdf.SetFont("Go", "B", 10)
	pdf.CellFormat(160, 8, "Total", "", 0, "R", false, 0, "")
	pdf.CellFormat(20, 8, fmtDuration(total(es)), "", 1, "R", false, 0, "")

	// Summary by task
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
	pdf.Ln(8)
	pdf.SetFont("Go", "B", 12)
	pdf.CellFormat(0, 8, "Summary by task", "", 1, "L", false, 0, "")
	pdf.SetFont("Go", "B", 10)
	pdf.SetFillColor(230, 230, 230)
	pdf.CellFormat(25, 8, "Task ID", "B", 0, "L", true, 0, "")
	pdf.CellFormat(135, 8, "Task", "B", 0, "L", true, 0, "")
	pdf.CellFormat(20, 8, "Hours", "B", 1, "R", true, 0, "")
	pdf.SetFont("Go", "", 10)
	for _, s := range sums {
		pdf.CellFormat(25, 7, s.id, "B", 0, "L", false, 0, "")
		pdf.CellFormat(135, 7, fit(pdf, s.name, 133), "B", 0, "L", false, 0, "")
		pdf.CellFormat(20, 7, fmtDuration(s.d), "B", 1, "R", false, 0, "")
	}

	return pdf.OutputFileAndClose(path)
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
