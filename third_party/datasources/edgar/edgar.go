// Package edgar is the independent SEC EDGAR data source: company filings and
// 13F holdings, parsed directly from SEC endpoints (no OpenBB). Fetch-only and
// stateless.
//
// SEC requires a descriptive User-Agent with contact info; UserAgent supplies a
// default and is sent on every request.
package edgar

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// SEC hosts. Vars (not const) so tests can point them at httptest servers.
var (
	SECBase     = "https://www.sec.gov"
	SECDataBase = "https://data.sec.gov"
)

// UserAgent is sent on every SEC request (SEC blocks generic/missing UAs).
var UserAgent = "nimbus-os data-gateway championdoggg@gmail.com"

// Filing is one recent submission.
type Filing struct {
	Form            string `json:"form"`
	FilingDate      string `json:"filing_date"`
	AccessionNumber string `json:"accession_number"`
	URL             string `json:"url"`
}

// Holding is one merged 13F position.
type Holding struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Shares float64 `json:"shares"`
}

// Period13F is one 13F-HR filing's holdings.
type Period13F struct {
	Accession  string    `json:"accession"`
	FilingDate string    `json:"filing_date"`
	ReportDate string    `json:"report_date"`
	Holdings   []Holding `json:"holdings"`
}

func get(ctx context.Context, url string) ([]byte, error) {
	return httpx.Default.GetBytes(ctx, url, map[string]string{
		"User-Agent": UserAgent,
		"Accept":     "application/json",
	})
}

// CIKForTicker resolves a ticker to its SEC CIK via the company-ticker map.
func CIKForTicker(ctx context.Context, ticker string) (int, error) {
	body, err := get(ctx, SECBase+"/files/company_tickers.json")
	if err != nil {
		return 0, err
	}
	var m map[string]struct {
		CIKStr int    `json:"cik_str"`
		Ticker string `json:"ticker"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return 0, fmt.Errorf("edgar ticker map: %w", err)
	}
	wanted := strings.ToUpper(ticker)
	for _, row := range m {
		if strings.ToUpper(row.Ticker) == wanted {
			return row.CIKStr, nil
		}
	}
	return 0, fmt.Errorf("ticker %q not found in SEC ticker map", ticker)
}

type submissions struct {
	Filings struct {
		Recent struct {
			Form            []string `json:"form"`
			FilingDate      []string `json:"filingDate"`
			AccessionNumber []string `json:"accessionNumber"`
			PrimaryDocument []string `json:"primaryDocument"`
			ReportDate      []string `json:"reportDate"`
		} `json:"recent"`
	} `json:"filings"`
}

func fetchSubmissions(ctx context.Context, cik10 string) (*submissions, error) {
	body, err := get(ctx, fmt.Sprintf("%s/submissions/CIK%s.json", SECDataBase, cik10))
	if err != nil {
		return nil, err
	}
	var s submissions
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("edgar submissions: %w", err)
	}
	return &s, nil
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

// RecentFilings returns up to limit of a CIK's most recent filings.
func RecentFilings(ctx context.Context, cik, limit int) ([]Filing, error) {
	s, err := fetchSubmissions(ctx, fmt.Sprintf("%010d", cik))
	if err != nil {
		return nil, err
	}
	r := s.Filings.Recent
	n := limit
	if len(r.Form) < n {
		n = len(r.Form)
	}
	out := make([]Filing, 0, n)
	for i := 0; i < n; i++ {
		acc := strings.ReplaceAll(at(r.AccessionNumber, i), "-", "")
		doc := at(r.PrimaryDocument, i)
		url := ""
		if acc != "" && doc != "" {
			url = fmt.Sprintf("%s/Archives/edgar/data/%d/%s/%s", SECBase, cik, acc, doc)
		}
		out = append(out, Filing{
			Form:            r.Form[i],
			FilingDate:      at(r.FilingDate, i),
			AccessionNumber: at(r.AccessionNumber, i),
			URL:             url,
		})
	}
	return out, nil
}

// Holdings13F returns the latest `periods` 13F-HR filings' merged holdings.
// limit caps holdings per period (0 = all), sorted by value descending.
func Holdings13F(ctx context.Context, cik string, periods, limit int) ([]Period13F, error) {
	cik10 := fmt.Sprintf("%010d", parseCIK(cik))
	s, err := fetchSubmissions(ctx, cik10)
	if err != nil {
		return nil, err
	}
	r := s.Filings.Recent
	out := []Period13F{}
	for i, form := range r.Form {
		if form != "13F-HR" {
			continue
		}
		reportDate := at(r.ReportDate, i)
		if reportDate == "" {
			reportDate = at(r.FilingDate, i)
		}
		accession := at(r.AccessionNumber, i)
		holdings, err := infoTable(ctx, cik10, accession)
		if err != nil {
			return nil, err
		}
		sort.Slice(holdings, func(a, b int) bool { return holdings[a].Value > holdings[b].Value })
		if limit > 0 && len(holdings) > limit {
			holdings = holdings[:limit]
		}
		out = append(out, Period13F{
			Accession:  accession,
			FilingDate: at(r.FilingDate, i),
			ReportDate: reportDate,
			Holdings:   holdings,
		})
		if len(out) >= periods {
			break
		}
	}
	return out, nil
}

var infoTableNameRE = regexp.MustCompile(`(?i)(info.*table|table.*info|infotable)`)

// infoTable fetches and parses a 13F filing's information table XML into merged
// holdings. Missing/unparseable filing → nil (not an error), matching the
// source's tolerance for filings without a standard info table.
func infoTable(ctx context.Context, cik10, accession string) ([]Holding, error) {
	if accession == "" {
		return nil, nil
	}
	accNoDash := strings.ReplaceAll(accession, "-", "")
	cikInt := strconv.Itoa(parseCIK(cik10))
	base := fmt.Sprintf("%s/Archives/edgar/data/%s/%s", SECBase, cikInt, accNoDash)

	body, err := get(ctx, base+"/index.json")
	if err != nil {
		return nil, nil // filing index unavailable
	}
	var idx struct {
		Directory struct {
			Item []struct {
				Name string `json:"name"`
			} `json:"item"`
		} `json:"directory"`
	}
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, nil
	}

	xmlName := ""
	for _, it := range idx.Directory.Item {
		if infoTableNameRE.MatchString(it.Name) && strings.HasSuffix(strings.ToLower(it.Name), ".xml") {
			xmlName = it.Name
			break
		}
	}
	if xmlName == "" {
		for _, it := range idx.Directory.Item {
			n := strings.ToLower(it.Name)
			if strings.HasSuffix(n, ".xml") && !strings.Contains(n, "primary_doc") {
				xmlName = it.Name
				break
			}
		}
	}
	if xmlName == "" {
		return nil, nil
	}

	xmlBody, err := get(ctx, base+"/"+xmlName)
	if err != nil {
		return nil, nil
	}
	return parseInfoTable(xmlBody), nil
}

// parseInfoTable streams the info-table XML, ignoring namespaces, collecting
// each <infoTable>'s nameOfIssuer/value/sshPrnamt, then merges by issuer.
func parseInfoTable(data []byte) []Holding {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var raw []Holding
	var cur *Holding
	var field string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "infoTable":
				cur = &Holding{}
				field = ""
			case "nameOfIssuer", "value", "sshPrnamt":
				field = t.Name.Local
			default:
				field = ""
			}
		case xml.CharData:
			if cur != nil && field != "" {
				s := strings.TrimSpace(string(t))
				if s == "" {
					break
				}
				switch field {
				case "nameOfIssuer":
					cur.Name += s
				case "value":
					cur.Value = num(s)
				case "sshPrnamt":
					cur.Shares = num(s)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "infoTable" && cur != nil {
				if cur.Name != "" && cur.Value != 0 {
					raw = append(raw, *cur)
				}
				cur = nil
			}
			field = ""
		}
	}

	order := []string{}
	merged := map[string]*Holding{}
	for _, h := range raw {
		m, ok := merged[h.Name]
		if !ok {
			m = &Holding{Name: h.Name}
			merged[h.Name] = m
			order = append(order, h.Name)
		}
		m.Value += h.Value
		m.Shares += h.Shares
	}
	out := make([]Holding, 0, len(order))
	for _, name := range order {
		out = append(out, *merged[name])
	}
	return out
}

func num(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(s, ",", "")), 64)
	if err != nil {
		return 0
	}
	return v
}

// parseCIK extracts the integer CIK from a string (handles zero-padded forms).
func parseCIK(s string) int {
	n, _ := strconv.Atoi(strings.TrimLeft(strings.TrimSpace(s), "0"))
	return n
}
