package memorylayer

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
)

const DefaultUserID = "default"

type UserProfile struct {
	Name        string   `json:"name,omitempty"`
	Preferences []string `json:"preferences,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
	LastSession string   `json:"last_session,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

var (
	reName   = regexp.MustCompile(`(?i)\b(?:my name is|i am|i'm|call me)\s+([A-Za-z][A-Za-z0-9_-]{1,39})\b`)
	rePrefer = regexp.MustCompile(`(?i)\b(?:i prefer|please use|use)\s+([^.!?\n]{3,120})`)
	reAvoid  = regexp.MustCompile(`(?i)\b(?:don't|do not|avoid|never)\s+([^.!?\n]{3,120})`)
)

func GetUserProfile(db *sql.DB, tenantID, userID string) (UserProfile, error) {
	userID = sanitizeUserID(userID)
	var raw string
	var updated string
	err := db.QueryRow(`SELECT profile_json, updated_at FROM user_profiles WHERE tenant_id = ? AND user_id = ?`, tenantID, userID).Scan(&raw, &updated)
	if err == sql.ErrNoRows {
		return UserProfile{}, nil
	}
	if err != nil {
		return UserProfile{}, err
	}
	var p UserProfile
	_ = json.Unmarshal([]byte(raw), &p)
	p.UpdatedAt = updated
	return p, nil
}

func UpdateUserProfileFromEpisodic(db *sql.DB, tenantID, userID, sessionID, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	userID = sanitizeUserID(userID)
	p, err := GetUserProfile(db, tenantID, userID)
	if err != nil {
		return err
	}

	if m := reName.FindStringSubmatch(content); len(m) > 1 {
		p.Name = strings.TrimSpace(m[1])
	}
	p.Preferences = mergeSignals(p.Preferences, extractSignals(rePrefer, content)...)
	p.Constraints = mergeSignals(p.Constraints, extractSignals(reAvoid, content)...)
	p.LastSession = strings.TrimSpace(sessionID)
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	b, _ := json.Marshal(p)
	_, err = db.Exec(`INSERT INTO user_profiles (tenant_id, user_id, profile_json, updated_at) VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(tenant_id, user_id) DO UPDATE SET profile_json=excluded.profile_json, updated_at=datetime('now')`,
		tenantID, userID, string(b))
	return err
}

func ProfileSummary(p UserProfile, maxItems int) string {
	if maxItems <= 0 {
		maxItems = 4
	}
	parts := []string{}
	if strings.TrimSpace(p.Name) != "" {
		parts = append(parts, "name="+p.Name)
	}
	if len(p.Preferences) > 0 {
		n := min(len(p.Preferences), maxItems)
		parts = append(parts, "preferences="+strings.Join(p.Preferences[:n], "; "))
	}
	if len(p.Constraints) > 0 {
		n := min(len(p.Constraints), maxItems)
		parts = append(parts, "constraints="+strings.Join(p.Constraints[:n], "; "))
	}
	return strings.Join(parts, " | ")
}

func sanitizeUserID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return DefaultUserID
	}
	return v
}

func extractSignals(re *regexp.Regexp, content string) []string {
	matches := re.FindAllStringSubmatch(content, 3)
	var out []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		s := cleanSignal(m[1])
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func cleanSignal(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.Trim(s, " .,:;!?\"'")
	if len(s) < 3 {
		return ""
	}
	return s
}

func mergeSignals(base []string, add ...string) []string {
	set := map[string]struct{}{}
	for _, x := range base {
		x = cleanSignal(x)
		if x != "" {
			set[x] = struct{}{}
		}
	}
	for _, x := range add {
		x = cleanSignal(x)
		if x != "" {
			set[x] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
