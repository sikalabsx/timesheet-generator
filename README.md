# sikalabsx/timesheet-generator

Generate monthly PDF timesheets and yearly summaries from a YAML file.

## Usage

```
go run . [-f timesheet.local.yaml] [-o out] [-m 2024-06]
```

- `-f` input YAML (default `timesheet.local.yaml`)
- `-o` output directory (default `out`)
- `-m` generate only one month (`YYYY-MM`, no yearly summary); by default all months and years are generated

Output:

- `out/timesheet-<client>-<YYYY-MM>.pdf` monthly timesheet with all entries
- `out/timesheet-<client>-<YYYY>.pdf` yearly summary with hours per month

## Format

```yaml
meta:
  version: 1
client:
  name: MSTP
# optional, multi-line Markdown, printed on every page
header: |
  # SikaLabs s.r.o.
  Contract **no. 2024/017**, see [sikalabs.com](https://sikalabs.com)
# optional, multi-line Markdown, printed on every page (page number on the last line)
footer: |
  **Approved by:** ____________________
# optional, hide the "Summary by task" table (default false)
hide_summary_by_task: true
entries:
  # date + duration (Go duration format, e.g. 4h30m)
  - date: 2024-06-01
    duration: 4h30m
    task_name: Initial project setup
    task_id: MSTP-1
  # or from + to
  - from: 2024-06-02 11:25
    to: 2024-06-02 15:25
    task_name: Requirement analysis
    task_id: MSTP-2
  # or date + from/to as times (task_id is optional);
  # "to" earlier than "from" means the next day, e.g. 22:20 - 00:00
  - date: 2024-06-03
    from: '10:00'
    to: '12:05'
    task_name: Requirement analysis
```

### Markdown in header and footer

Supported: `# H1`, `## H2`, `### H3`, `- bullets`, `**bold**`, `*italic*` / `_italic_`,
`` `code` ``, `[links](https://example.com)`, autolinks `<https://sika.io>` / `<ondrej@sika.io>`
and `\` escapes. Each line is one line in the PDF.
