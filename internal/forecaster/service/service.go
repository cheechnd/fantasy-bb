package service

import (
	"context"
	"fmt"
	"time"

	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/forecaster/parser"
	"fantasy-baseball/internal/forecaster/source"
)

type Service struct {
	repo   *forecaster.Repository
	parser *parser.ForecasterParser
	file   source.Fetcher
	url    source.Fetcher
}

func New(repo *forecaster.Repository) *Service {
	return &Service{
		repo:   repo,
		parser: parser.New(),
		file:   source.FileSourceFetcher{},
		url:    source.NewURLSourceFetcher(),
	}
}

type ImportSummary struct {
	ImportRun          forecaster.ImportRun           `json:"import_run"`
	SourceType         forecaster.SourceType          `json:"source_type"`
	SourceIdentifier   string                         `json:"source_identifier"`
	RawRows            int                            `json:"raw_row_count"`
	ProbableStartCount int                            `json:"probable_start_count"`
	WarningCount       int                            `json:"warning_count"`
	Warnings           []forecaster.ParseWarningInput `json:"warnings"`
}

func (s *Service) ImportFromFile(ctx context.Context, path string) (ImportSummary, error) {
	return s.importWithFetcher(ctx, forecaster.SourceTypeFile, path, s.file)
}

func (s *Service) ImportFromURL(ctx context.Context, rawURL string) (ImportSummary, error) {
	return s.importWithFetcher(ctx, forecaster.SourceTypeURL, rawURL, s.url)
}

func (s *Service) importWithFetcher(ctx context.Context, sourceType forecaster.SourceType, identifier string, fetcher source.Fetcher) (ImportSummary, error) {
	raw, err := fetcher.Fetch(ctx, identifier)
	if err != nil {
		return ImportSummary{}, err
	}
	parsed, err := s.parser.Parse(raw)
	if err != nil {
		run, insertErr := s.repo.InsertImport(ctx, sourceType, identifier, 0, nil, []forecaster.ParseWarningInput{{WarningType: "parse_error", Message: err.Error()}}, "failed", "{}")
		if insertErr != nil {
			return ImportSummary{}, fmt.Errorf("parse failed (%v) and failed to store failed run (%w)", err, insertErr)
		}
		return ImportSummary{ImportRun: run, SourceType: sourceType, SourceIdentifier: identifier}, err
	}

	run, err := s.repo.InsertImport(ctx, sourceType, identifier, parsed.RawRowCount, parsed.Starts, parsed.Warnings, "success", "{}")
	if err != nil {
		return ImportSummary{}, err
	}
	return ImportSummary{
		ImportRun:          run,
		SourceType:         sourceType,
		SourceIdentifier:   identifier,
		RawRows:            parsed.RawRowCount,
		ProbableStartCount: len(parsed.Starts),
		WarningCount:       len(parsed.Warnings),
		Warnings:           parsed.Warnings,
	}, nil
}

func (s *Service) List(ctx context.Context, filter forecaster.ListFilter) ([]forecaster.ProbableStart, error) {
	return s.repo.ListProbableStarts(ctx, filter)
}

func (s *Service) ShowWeek(ctx context.Context, fromDate string, includeTBD bool) ([]forecaster.ProbableStart, error) {
	from, err := parseOptionalDate(fromDate)
	if err != nil {
		return nil, err
	}
	if from == nil {
		now := nowDate()
		from = &now
	}
	to := from.AddDate(0, 0, 6)
	return s.repo.ListProbableStarts(ctx, forecaster.ListFilter{
		From:       from,
		To:         &to,
		IncludeTBD: includeTBD,
	})
}

func (s *Service) Top(ctx context.Context, filter forecaster.TopFilter) ([]forecaster.ProbableStart, error) {
	return s.repo.TopProbableStarts(ctx, filter)
}

func (s *Service) SourceStatus(ctx context.Context, limit int) ([]forecaster.ImportRun, error) {
	return s.repo.SourceStatus(ctx, limit)
}

func (s *Service) Warnings(ctx context.Context, importRunID *int64, limit int) ([]forecaster.ParseWarning, error) {
	return s.repo.ListWarnings(ctx, importRunID, limit)
}

func (s *Service) LatestImport(ctx context.Context) (*forecaster.ImportRun, error) {
	return s.repo.LatestImportRun(ctx)
}

func (s *Service) Clear(ctx context.Context) (forecaster.ClearResult, error) {
	return s.repo.Clear(ctx)
}

func parseOptionalDate(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	tm, err := time.ParseInLocation("2006-01-02", raw, time.Local)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q, expected YYYY-MM-DD", raw)
	}
	tm = time.Date(tm.Year(), tm.Month(), tm.Day(), 0, 0, 0, 0, time.Local)
	return &tm, nil
}

func nowDate() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.Local)
}
