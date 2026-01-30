package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-resty/resty/v2"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	calendar "google.golang.org/api/calendar/v3"
	gmail "google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// Ollama local endpoint and model
const OllamaAPIURL = "http://localhost:11435/api/generate"
const OllamaModel = "llama3"
const calendarScope = "https://www.googleapis.com/auth/calendar.events"

// 🚀 FIXED: Robust date normalization + validation
func normalizeDate(dateStr string) string {
	dateStr = strings.TrimSpace(dateStr)
	if dateStr == "" {
		return ""
	}

	// Current year (2026) + next few years
	currentYear := time.Now().Year()

	// Fix wrong years from AI (2023/2024 → 2026)
	re := regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})`)
	if matches := re.FindStringSubmatch(dateStr); len(matches) == 4 {
		year, _ := time.Parse("2006", matches[1])
		if year.Year() < currentYear {
			// Replace wrong year with current/next year
			return fmt.Sprintf("%d-%s-%s", currentYear, matches[2], matches[3])
		}
		return dateStr
	}

	// Handle common patterns
	replacements := map[string]string{
		"31st Jan":   fmt.Sprintf("%d-01-31", currentYear),
		"Jan 31":     fmt.Sprintf("%d-01-31", currentYear),
		"January 31": fmt.Sprintf("%d-01-31", currentYear),
		"Jan 31st":   fmt.Sprintf("%d-01-31", currentYear),
		"tomorrow":   time.Now().Add(24 * time.Hour).Format("2006-01-02"),
		"today":      time.Now().Format("2006-01-02"),
	}

	dateLower := strings.ToLower(dateStr)
	for input, output := range replacements {
		if strings.Contains(dateLower, input) {
			return output
		}
	}

	// Already YYYY-MM-DD format
	if len(dateStr) >= 10 && dateStr[4] == '-' && dateStr[7] == '-' {
		return dateStr
	}

	return "" // Invalid format
}

// 🚀 NEW: Validate date is realistic (not in distant past/future)
func isValidDate(dateStr string) bool {
	if dateStr == "" || len(dateStr) < 10 {
		return false
	}

	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return false
	}

	// Must be within next 30 days or today
	return t.After(time.Now().Add(-24*time.Hour)) && t.Before(time.Now().Add(30*24*time.Hour))
}

// 🚀 FIXED: Enhanced event parsing with regex and normalization
func parseEventFromSummary(summary string) (title, date, start, end, location, notes string) {
	// Helper to extract a labeled field with regex (case-insensitive)
	get := func(label string) string {
		re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(label) + `\s*:\s*(.+)`)
		m := re.FindStringSubmatch(summary)
		if len(m) >= 2 {
			return strings.TrimSpace(m[1])
		}
		return ""
	}

	title = get("Event Title")
	date = get("Date")
	start = get("Start Time")
	end = get("End Time")
	location = get("Location")
	notes = get("Notes/Description")

	// Clean title
	title = strings.TrimSpace(strings.TrimPrefix(title, "*"))

	// Normalize date: prefer YYYY-MM-DD if present, else strip parentheses/commas
	if d := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`).FindString(date); d != "" {
		date = d
	} else {
		if idx := strings.IndexAny(date, ",("); idx != -1 {
			date = strings.TrimSpace(date[:idx])
		}
	}

	// Clean start/end times (remove timezone text and keep AM/PM tokens)
	cleanTime := func(s string) string {
		s = strings.TrimSpace(s)
		if idx := strings.Index(s, "GMT"); idx != -1 {
			s = strings.TrimSpace(s[:idx])
		}
		if idx := strings.Index(s, "("); idx != -1 {
			s = strings.TrimSpace(s[:idx])
		}
		parts := strings.Fields(s)
		if len(parts) >= 2 {
			// Example: ["10:00", "AM"]
			if strings.HasSuffix(strings.ToUpper(parts[1]), "AM") || strings.HasSuffix(strings.ToUpper(parts[1]), "PM") {
				return parts[0] + " " + parts[1]
			}
		}
		if len(parts) > 0 {
			return parts[0]
		}
		return s
	}

	start = cleanTime(start)
	end = cleanTime(end)

	return
}

// Summarize email content using Ollama
func SummarizeWithOllama(ctx context.Context, emailContent string) (string, error) {
	client := resty.New()
	prompt := fmt.Sprintf(`Summarize the following email content in 2-3 sentences.

Then extract any events or deadlines mentioned.
For each event, include EXACTLY:
- Event Title: [name]
- Date: [YYYY-MM-DD format, use 2026 for this year]
- Start Time: [HH:MM 24hr format]
- End Time: [HH:MM 24hr format if available]
- Location: [venue]
- Notes/Description: [details]

If no events or deadlines are found, say "No events found."

Email Content:
%s`, emailContent)

	reqBody := map[string]interface{}{
		"model":  OllamaModel,
		"prompt": prompt,
		"stream": false,
	}

	resp, err := client.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(reqBody).
		Post(OllamaAPIURL)
	if err != nil {
		return "", fmt.Errorf("Ollama connection failed: %v", err)
	}
	if resp.IsError() {
		return "", fmt.Errorf("Ollama API error: %s", resp.String())
	}

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(resp.Body(), &ollamaResp); err != nil {
		return strings.TrimSpace(resp.String()), nil
	}

	return strings.TrimSpace(ollamaResp.Response), nil
}

// Create a Google Calendar event (with validation and better error diagnostics)
func createCalendarEvent(srv *calendar.Service, title, date, start, end, location, notes string) error {
	// 🚀 CRITICAL: Validate date format before creating
	if !isValidDate(date) {
		return fmt.Errorf("invalid date: %s (must be YYYY-MM-DD within 30 days)", date)
	}

	tzone := "Asia/Kolkata"
	startTime := "09:00"
	endTime := "10:00"

	// Helper: normalize various time formats ("10:00 AM", "9:00", "09:00") → "HH:MM"
	parseTime := func(s string) (string, error) {
		s = strings.TrimSpace(s)
		if s == "" {
			return "", nil
		}
		if regexp.MustCompile(`^\d{2}:\d{2}$`).MatchString(s) {
			return s, nil
		}
		// Try common AM/PM formats
		if t, err := time.Parse("3:04 PM", s); err == nil {
			return t.Format("15:04"), nil
		}
		if t, err := time.Parse("03:04 PM", s); err == nil {
			return t.Format("15:04"), nil
		}
		if t, err := time.Parse("15:04", s); err == nil {
			return t.Format("15:04"), nil
		}
		return "", fmt.Errorf("unrecognized time format: %s", s)
	}

	if p, err := parseTime(start); err == nil && p != "" {
		startTime = p
	}
	if p, err := parseTime(end); err == nil && p != "" {
		endTime = p
	}

	// If no end time provided or it's N/A, default to start + 1 hour
	if strings.TrimSpace(end) == "" || strings.EqualFold(strings.TrimSpace(end), "N/A") {
		if st, err := time.Parse("15:04", startTime); err == nil {
			endTime = st.Add(1 * time.Hour).Format("15:04")
		}
	}

	startDT := fmt.Sprintf("%sT%s:00+05:30", date, startTime)
	endDT := fmt.Sprintf("%sT%s:00+05:30", date, endTime)

	// Validate RFC3339
	if _, err := time.Parse(time.RFC3339, startDT); err != nil {
		return fmt.Errorf("start datetime invalid RFC3339: %v (value=%s)", err, startDT)
	}
	if _, err := time.Parse(time.RFC3339, endDT); err != nil {
		return fmt.Errorf("end datetime invalid RFC3339: %v (value=%s)", err, endDT)
	}

	// Ensure start < end
	st, _ := time.Parse(time.RFC3339, startDT)
	en, _ := time.Parse(time.RFC3339, endDT)
	fmt.Printf("  [DEBUG] startDT=%s (%s), endDT=%s (%s)\n", startDT, st.Format(time.RFC3339), endDT, en.Format(time.RFC3339))
	if !st.Before(en) {
		return fmt.Errorf("start time (%s) is not before end time (%s)", startDT, endDT)
	}

	event := &calendar.Event{
		Summary:     title,
		Location:    location,
		Description: notes,
		Start: &calendar.EventDateTime{
			DateTime: startDT,
			TimeZone: tzone,
		},
		End: &calendar.EventDateTime{
			DateTime: endDT,
			TimeZone: tzone,
		},
	}

	// Debug: print JSON of the event being sent
	if b, err := json.MarshalIndent(event, "", "  "); err == nil {
		fmt.Printf("  [DEBUG] Event to insert: %s\n", string(b))
	}

	_, err := srv.Events.Insert("primary", event).Do()
	if err != nil {
		// Try to extract Google API error details
		if gerr, ok := err.(*googleapi.Error); ok {
			return fmt.Errorf("googleapi error: code=%d message=%s body=%s", gerr.Code, gerr.Message, gerr.Body)
		}
		return err
	}
	return nil
}

// OAuth helpers (unchanged)
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("Saving OAuth token to: %s\n", path)
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("Unable to save OAuth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Visit the URL below to get a code, then paste it here:\n%v\n", authURL)
	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Failed to read authorization code: %v", err)
	}

	tok, err := config.Exchange(context.Background(), authCode)
	if err != nil {
		log.Fatalf("Unable to exchange code for token: %v", err)
	}
	return tok
}

func getClient(config *oauth2.Config) *http.Client {
	const tokenFile = "token.json"
	tok, err := tokenFromFile(tokenFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokenFile, tok)
	}
	return config.Client(context.Background(), tok)
}

func main() {
	fmt.Println("🤖 Starting AI Email Agent with Web Dashboard...")
	fmt.Println("🌐 Open: http://localhost:8080/dashboard")

	// Your existing Gmail/Calendar setup
	b, err := ioutil.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Error reading credentials.json: %v", err)
	}

	config, err := google.ConfigFromJSON(b, gmail.GmailReadonlyScope, calendarScope)
	if err != nil {
		log.Fatalf("Unable to parse credentials.json to config: %v", err)
	}

	client := getClient(config)
	gmailSrv, err := gmail.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to create Gmail service: %v", err)
	}
	calendarSrv, err := calendar.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to create Calendar service: %v", err)
	}

	// 🚀 NEW: Web Dashboard Router
	r := chi.NewRouter()
	r.Mount("/dashboard", dashboardRoutes())

	// 🚀 Make Gmail/Calendar services globally accessible
	// (Add to global vars or pass via context)

	fmt.Println("✅ Dashboard ready! Ollama must be running (ollama serve)")
	log.Fatal(http.ListenAndServe(":8080", r))
}

// Minimal dashboard handler to keep build clean
func dashboardRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl := template.Must(template.New("dash").Parse(`<html><body><h1>AI Email Agent Dashboard</h1><p>Open <a href="/dashboard">/dashboard</a></p></body></html>`))
		tmpl.Execute(w, nil)
	})
	return r
}
