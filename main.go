package main

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MatusOllah/slogcolor"
	"github.com/fatih/color"
)

func main() {
	//this means get the current time so that we can track how long it takes
	tAllstart := time.Now()
	//this sets up the options for the logger which prints to the teminals
	opts := slogcolor.DefaultOptions
	//opts.Level = slog.LevelDebug
	opts.MsgColor = color.New(color.FgMagenta)
	opts.SrcFileMode = slogcolor.Nop
	logger := slog.New(slogcolor.NewHandler(os.Stderr, opts))

	//this is the print statement equivelent
	logger.Info("Reading config")
	cfg, err := LoadConfig()
	if err != nil {
		fail(err)
	}

	//prints reading pdfs, reads pdfs and reurns an error if err is not empty (an error exists) then it returns an error
	logger.Info("Reading PDFs")
	//pdfs is a dict of the pdf file names and the actual pdf content
	pdfs, err := readPDFsFromDir("./pdf")
	if err != nil {
		fail(err)
	}
	//dictorionaries arent naturally ordeed so this creates two lists and inserts the dictionaries to keep them in the order we want
	pdfNames := make([]string, 0, len(pdfs))
	pdfContents := make([]string, 0, len(pdfs))
	for k, v := range pdfs {
		pdfNames = append(pdfNames, k)
		pdfContents = append(pdfContents, v)
	}
	for i, path := range pdfNames {
		//logger.debug is a level of logger that doesn't actually print it out, unless u uncomment line 23 which would then let us see this level of logger in the terminal too
		logger.Debug("Loaded PDF", "index", i, "path", path)
	}

	if err := os.MkdirAll("./result", os.ModePerm); err != nil {
		fail(err)
	}

	logger.Info("Creating model builder")
	modelBuilder, err := BuildModelBuilder(cfg.APIKey)
	if err != nil {
		fail(err)
	}

	wg := &sync.WaitGroup{}
	wg.Add(len(cfg.Views))

	for viewName, view := range cfg.Views {
		go func() {
			defer wg.Done()
			tstart := time.Now()
			checklist := checklistFromConfig(view)
			viewLogger := logger.With("view_name", viewName)
			viewLogger.Info("Beginning review", "num_checklist", len(checklist), "num_resumes", len(pdfContents))
			result, err := ReviewCandidates(modelBuilder, checklist, pdfContents, viewLogger)
			if err != nil {
				fail(err)
			}
			reports := make([]CandidateReport, len(result))
			for i := range result {
				finalScore := 0.0
				for key, cqc := range result[i] {
					if !cqc.IsTrue() {
						continue
					}
					finalScore += view.ScoreChecklist[key].Weight
				}
				reports[i] = CandidateReport{
					FileName:   filepath.Base(pdfNames[i]),
					FileLoc:    pdfNames[i],
					Checklist:  result[i],
					FinalScore: finalScore,
				}
			}
			sort.Slice(reports, func(i, j int) bool {
				if reports[i].FinalScore > reports[j].FinalScore {
					return true
				} else if reports[i].FinalScore < reports[j].FinalScore {
					return false
				} else {
					return reports[i].FileName < reports[j].FileName
				}
			})
			//writes the cvs file 3 times for the 3 csvs it gives us
			err = WriteCandidateReportsAsCSVFile(fmt.Sprintf("./result/report_%s.csv", viewName), reports, Boolean)
			if err != nil {
				fail(err)
			}
			err = WriteCandidateReportsAsCSVFile(fmt.Sprintf("./result/probabilities_%s.csv", viewName), reports, Probability)
			if err != nil {
				fail(err)
			}
			err = WriteCandidateReportsAsCSVFile(fmt.Sprintf("./result/inconsistency_%s.csv", viewName), reports, Inconsistency)
			if err != nil {
				fail(err)
			}
			viewLogger.Info("Finished review", "time_taken", time.Since(tstart))
		}()
	}
	wg.Wait()
	logger.Info("Everything finished", "time_taken", time.Since(tAllstart))
}

func fail(args ...any) {
	fmt.Println(args...)
	os.Exit(1)
}

func checklistFromConfig(cfg ConfigView) map[string]string {
	checklist := make(map[string]string)
	for key, val := range cfg.ScoreChecklist {
		checklist[key] = val.Question
	}
	return checklist
}

func listPDFs(dir string) ([]string, error) {
	var pdfFiles []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
			pdfFiles = append(pdfFiles, path)
		}
		return nil
	})

	return pdfFiles, err
}

func readPDFsFromDir(dir string) (map[string]string, error) {
	fileNames, err := listPDFs(dir)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, fn := range fileNames {
		text, err := GetTextFromPDFFile(fn)
		if err != nil {
			return nil, err
		}
		result[fn] = text
	}
	return result, nil
}
