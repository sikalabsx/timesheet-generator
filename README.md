# sikalabsx/timesheet-generator

Generate monthly PDF timesheets from a YAML file.

## Usage

```
go run . [-f timesheet.local.yaml] [-o out] [-m 2024-06]
```

- `-f` input YAML (default `timesheet.local.yaml`)
- `-o` output directory (default `out`)
- `-m` generate only one month (`YYYY-MM`); by default all months are generated

Output: `out/timesheet-<client>-<YYYY-MM>.pdf`

## Format

```yaml
meta:
  version: 1
client:
  name: MSTP
# optional, multi-line, printed on every page
header: |
  SikaLabs s.r.o.
  Contract no. 2024/017
# optional, multi-line, printed on every page above the page number
footer: |
  Approved by: ____________________
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
```
