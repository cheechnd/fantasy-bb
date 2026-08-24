package relievers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	espnrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/pitchers/matching"
)

type Service struct {
	repo     *Repository
	espnRepo *espnrepo.Repository
	client   *http.Client
}

func NewService(repo *Repository, espnRepo *espnrepo.Repository) *Service {
	return &Service{repo: repo, espnRepo: espnRepo, client: &http.Client{Timeout: 20 * time.Second}}
}

type SyncOptions struct {
	SourceURL string
	DryRun    bool
}

func (s *Service) Sync(ctx context.Context, opts SyncOptions) (SyncSummary, error) {
	sourceURL := strings.TrimSpace(opts.SourceURL)
	if sourceURL == "" {
		sourceURL = DefaultSourceURL
	}
	fetchedAt := time.Now().UTC()
	raw, status, err := s.fetch(ctx, sourceURL)
	if err != nil {
		summary := SyncSummary{Source: SourceESPNRelieverDepthChart, SourceURL: sourceURL, FetchedAt: fetchedAt.Format(time.RFC3339), Status: "failed", Warnings: []string{err.Error()}, DryRun: opts.DryRun}
		if !opts.DryRun {
			_, _ = s.repo.SaveRun(ctx, SaveRunInput{Source: summary.Source, SourceURL: sourceURL, FetchedAt: fetchedAt, Status: "failed", Warnings: summary.Warnings, Summary: map[string]any{"http_status": status}, RawHTML: string(raw)})
		}
		return summary, err
	}
	parsed, parseErr := parseDepthChartHTML(raw)
	entries := parsed.Rows
	warnings := append([]string{}, parsed.Warnings...)
	if parseErr == nil {
		entries, warnings = s.matchEntries(ctx, entries, warnings)
	} else {
		warnings = append(warnings, parseErr.Error())
	}
	matched, unmatched, ambiguous, conflicts := countStatuses(entries)
	runStatus := "success"
	if parseErr != nil {
		runStatus = "failed"
	}
	summary := SyncSummary{
		Source:         SourceESPNRelieverDepthChart,
		SourceURL:      sourceURL,
		SourceDate:     parsed.SourceDate,
		FetchedAt:      fetchedAt.Format(time.RFC3339),
		Status:         runStatus,
		TeamCount:      parsed.Teams,
		RowCount:       len(entries),
		MatchedCount:   matched,
		UnmatchedCount: unmatched,
		AmbiguousCount: ambiguous,
		ConflictCount:  conflicts,
		Warnings:       warnings,
		DryRun:         opts.DryRun,
	}
	if opts.DryRun {
		if parseErr != nil {
			return summary, parseErr
		}
		return summary, nil
	}
	runID, saveErr := s.repo.SaveRun(ctx, SaveRunInput{
		Source:     SourceESPNRelieverDepthChart,
		SourceURL:  sourceURL,
		SourceDate: parsed.SourceDate,
		FetchedAt:  fetchedAt,
		Status:     runStatus,
		Warnings:   warnings,
		Summary: map[string]any{
			"team_count":      parsed.Teams,
			"row_count":       len(entries),
			"matched_count":   matched,
			"unmatched_count": unmatched,
			"ambiguous_count": ambiguous,
			"conflict_count":  conflicts,
			"http_status":     status,
		},
		RawHTML: string(raw),
		Entries: entries,
	})
	if saveErr != nil {
		return SyncSummary{}, saveErr
	}
	summary.RunID = &runID
	if parseErr != nil {
		return summary, parseErr
	}
	return summary, nil
}

func (s *Service) Show(ctx context.Context, runID *int64, limit int) (*DepthChartRun, []DepthChartEntry, error) {
	var run *DepthChartRun
	var err error
	if runID == nil {
		run, err = s.repo.LatestRun(ctx)
	} else {
		// Entries can be selected by run id; status output remains latest-list focused.
		runs, listErr := s.repo.ListRuns(ctx, 500)
		if listErr != nil {
			return nil, nil, listErr
		}
		for _, r := range runs {
			if r.ID == *runID {
				rr := r
				run = &rr
				break
			}
		}
	}
	if err != nil || run == nil {
		return run, []DepthChartEntry{}, err
	}
	id := run.ID
	entries, err := s.repo.Entries(ctx, &id, limit)
	return run, entries, err
}

func (s *Service) Status(ctx context.Context, limit int) ([]DepthChartRun, error) {
	return s.repo.ListRuns(ctx, limit)
}

func (s *Service) Enrich(ctx context.Context, playerID *int64, normalizedName, mlbTeam string) (Enrichment, error) {
	entry, run, err := s.repo.LatestEntryByPlayer(ctx, playerID, normalizedName, strings.ToUpper(strings.TrimSpace(mlbTeam)))
	if err != nil || entry == nil || run == nil || run.Status != "success" {
		return Enrichment{}, err
	}
	return Enrichment{
		ReliefRole:       entry.ReliefRole,
		ReliefRoleTeam:   entry.MLBTeam,
		ReliefRoleSource: run.Source,
		ReliefRoleAsOf:   firstNonEmpty(run.SourceDate, run.FetchedAt.Format(time.RFC3339)),
		MatchStatus:      entry.MatchStatus,
		ConflictFlag:     entry.ConflictFlag,
		ConflictReason:   entry.ConflictReason,
	}, nil
}

func (s *Service) fetch(ctx context.Context, sourceURL string) ([]byte, int, error) {
	client := s.client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create reliever depth chart request: %w", err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "fantasy-baseball/fb relievers-readonly")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch reliever depth chart: %w", err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return raw, resp.StatusCode, fmt.Errorf("read reliever depth chart response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, resp.StatusCode, fmt.Errorf("reliever depth chart request failed with status %d", resp.StatusCode)
	}
	return raw, resp.StatusCode, nil
}

func (s *Service) matchEntries(ctx context.Context, entries []DepthChartEntry, warnings []string) ([]DepthChartEntry, []string) {
	roster, _ := s.espnRepo.LatestRoster(ctx, nil, true)
	candidates, _ := s.espnRepo.ListCandidates(ctx, nil, 1000)
	byID := map[int64]int{}
	byNameTeam := map[string][]int{}
	for i := range entries {
		if entries[i].ESPNPlayerID != nil {
			byID[*entries[i].ESPNPlayerID] = i
		}
		key := entries[i].NormalizedName + "|" + entries[i].MLBTeam
		byNameTeam[key] = append(byNameTeam[key], i)
	}
	mark := func(playerID *int64, normalizedName, team, source string) {
		idxs := []int{}
		if playerID != nil {
			if idx, ok := byID[*playerID]; ok {
				idxs = []int{idx}
			}
		}
		if len(idxs) == 0 {
			idxs = byNameTeam[normalizedName+"|"+strings.ToUpper(strings.TrimSpace(team))]
		}
		if len(idxs) == 0 {
			return
		}
		for _, idx := range idxs {
			if entries[idx].MatchStatus == "matched" && entries[idx].MatchReason != source {
				entries[idx].MatchStatus = "ambiguous"
				entries[idx].MatchReason = "matched multiple local player sources"
				entries[idx].ConflictFlag = true
				entries[idx].ConflictReason = "multiple_local_matches"
				continue
			}
			entries[idx].MatchStatus = "matched"
			entries[idx].MatchReason = source
		}
	}
	for _, row := range roster {
		mark(row.ESPNPlayerID, row.NormalizedName, row.MLBTeam, "latest_roster_snapshot")
	}
	for _, row := range candidates {
		mark(row.ESPNPlayerID, row.NormalizedName, row.MLBTeam, "latest_free_agent_candidate_snapshot")
	}
	return entries, warnings
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func NormalizeName(name string) string { return matching.NormalizeName(name) }

func EnrichmentForEntry(entry DepthChartEntry, run DepthChartRun) Enrichment {
	return Enrichment{ReliefRole: entry.ReliefRole, ReliefRoleTeam: entry.MLBTeam, ReliefRoleSource: run.Source, ReliefRoleAsOf: firstNonEmpty(run.SourceDate, run.FetchedAt.Format(time.RFC3339)), MatchStatus: entry.MatchStatus, ConflictFlag: entry.ConflictFlag, ConflictReason: entry.ConflictReason}
}
