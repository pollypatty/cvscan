package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/JoshPattman/jpf"
)

//an interface says that the variables can be any type as long as it has this function on it(you could do x.function)
type CandidateReviewModelBuilder interface {
	BuildCandidateReviewModel(*slog.Logger) jpf.Model
}

//if there is no comma in between then ur classifyng a type, if there is a comma its two sep things (e.g. this takes in an apikey which is a string, and returns candidateblablabla AND and error)
func BuildModelBuilder(apiKey string) (CandidateReviewModelBuilder, error) {
	//jpf is the llm handling thing
	//here we are trying to create a new object (cache) which creates a new files and this file is cache.gob
	cache, err := jpf.NewFilePersistCache("./cache.gob")
	//checks err is empty
	if err != nil {
		return nil, err
	}
	//creates an instance of the class that is defined in the little paragraph below
	return &dumbModelBuilder{
		apiKey:      apiKey,
		concLimiter: jpf.NewMaxConcurrentLimiter(3),
		cache:       cache,
	}, nil
}

//creates the dumb model builder class
type dumbModelBuilder struct {
	apiKey      string
	concLimiter jpf.ConcurrentLimiter
	cache       jpf.ModelResponseCache
}

//this creates a function (build candidate blablabla) on the dumb model builder class
func (mb *dumbModelBuilder) BuildCandidateReviewModel(logger *slog.Logger) jpf.Model {
	model := jpf.NewOpenAIModel(mb.apiKey, "gpt-4.1", jpf.WithTemperature{X: 0})
	model = jpf.NewLoggingModel(model, jpf.NewSlogModelLogger(logger.Info, false))
	model = jpf.NewRetryModel(model, 8, jpf.WithDelay{X: time.Second * 5})
	model = jpf.NewConcurrentLimitedModel(model, mb.concLimiter)
	model = jpf.NewCachedModel(model, mb.cache)
	return model
}

type CandidateReviewData struct {
	I         int
	Checklist map[string]string
	Resume    string
}

type CandidateReviewer jpf.MapFunc[CandidateReviewData, map[string]bool]

//a class that just contains a probability
type CandidateQuestionResult struct {
	Probability float64
}

//returns true if the probability of it being true is more than 0.5
func (c CandidateQuestionResult) IsTrue() bool {
	return c.Probability > 0.5
}

//how far away from sure/positive was the llm? - good measure of inconsistency
func (c CandidateQuestionResult) Inconsistency() float64 {
	return min(c.Probability, 1-c.Probability) * 2
}

//this func takes a list of candidates
func ReviewCandidates(modelBuilder CandidateReviewModelBuilder, checklist map[string]string, resumes []string, logger *slog.Logger) ([]map[string]CandidateQuestionResult, error) {
	results := make([]map[string]CandidateQuestionResult, len(resumes))
	errs := make([]error, len(results))
	wg := &sync.WaitGroup{}
	wg.Add(len(results))
	for i := range resumes {
		go func() {
			candidateLogger := logger.With("resume", i)
			res, err := ReviewCandidate(modelBuilder, checklist, resumes[i], 10, candidateLogger)
			if err != nil {
				errs[i] = err
				candidateLogger.Error("Failed to review candidate", "err", err)
			} else {
				inconsistency := 0.0
				for _, v := range res {
					inconsistency += v.Inconsistency()
				}
				inconsistency /= float64(len(res))
				results[i] = res
				candidateLogger.Debug("Completed candidate review", "result", results[i])
				candidateLogger.Info("Completed candidate review", "inconsistency", math.Round(inconsistency*100)/100)
			}
			wg.Done()
		}()
	}
	wg.Wait()
	errs = slices.DeleteFunc(errs, func(err error) bool { return err == nil })
	if len(errs) != 0 {
		return nil, errors.Join(errs...)
	}
	return results, nil
}

//this function repeats the calls 10 times and assesses accuracy, applies to each candidate 1 by 1
//this func returns a dictionary with question keys mapping to results
func ReviewCandidate(modelBuilder CandidateReviewModelBuilder, checklist map[string]string, resume string, repeats int, logger *slog.Logger) (map[string]CandidateQuestionResult, error) {
	resultss := make([]map[string]bool, repeats)
	errs := make([]error, repeats)
	//wg(wait group) is a counter
	wg := &sync.WaitGroup{}
	//this adds repeats (whicgh is an int) to the counter
	wg.Add(repeats)
	for i := range repeats {
		//go func runs the code inside func in another thread - in paralell
		go func() {
			//defer ,eans that when this function returns run the defer bit (which reduces wg by 1)
			defer wg.Done()
			//here i is the repeat no. not the candidate no. like it usually is
			repLogger := logger.With("repeat", i)
			results, err := reviewCandidateHelper(modelBuilder, checklist, resume, i, repLogger)
			if err != nil {
				errs[i] = err
				repLogger.Error("failed to review candidate", "err", err)
			} else {
				resultss[i] = results
			}
		}()
	}
	//this waits for the counter wg to get to 0
	wg.Wait()
	errs = slices.DeleteFunc(errs, func(err error) bool { return err == nil })
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	//this part sums up the trues and false of the repeats to make the probabilities
	probs := make(map[string]CandidateQuestionResult)
	for _, results := range resultss {
		for k, v := range results {
			var delta float64
			if v {
				delta = 1
			}
			probs[k] = CandidateQuestionResult{
				probs[k].Probability + delta,
			}
		}
	}
	for k, v := range probs {
		probs[k] = CandidateQuestionResult{
			v.Probability / float64(repeats),
		}
	}
	return probs, nil
}

//this function calls the llm by sending the message to jpf and parses the results (parsing means converting the string to structured data)
func reviewCandidateHelper(modelBuilder CandidateReviewModelBuilder, checklist map[string]string, resume string, i int, logger *slog.Logger) (map[string]bool, error) {
	//enc makes classes into llm text (so the llm can understand) - encoder
	enc := jpf.NewTemplateMessageEncoder[CandidateReviewData]("", simpleCandidateReviewTemplate)
	//dec makes llm text into classes (so we can understand) - decoder
	//dec decodes what the llm responds with into a dictionary with string keys and checklist item response values(from the class)
	dec := jpf.NewJsonResponseDecoder[map[string]checklistItemResponse]()
	dec = jpf.NewValidatingResponseDecoder(dec, func(m map[string]checklistItemResponse) error {
		missingKeys := make([]string, 0)
		for k := range checklist {
			if _, ok := m[k]; !ok {
				missingKeys = append(missingKeys, k)
			}
		}
		if len(missingKeys) > 0 {
			return fmt.Errorf("missing the following question keys: %v", missingKeys)
		}
		return nil
	})
	//fed gives the error text from above to the llm if it responds with something wrong so that the llm will try again
	fed := jpf.NewRawMessageFeedbackGenerator()
	model := modelBuilder.BuildCandidateReviewModel(logger)
	//this mf is not connected to the other mf - josh is just bad at naming things
	mf := jpf.NewFeedbackMapFunc(enc, dec, fed, model, jpf.UserRole, 10)

	//this calls the llm - it takes the candidate review data, converts to llm message (using enc) and then sends to the llm (llm in question is in the model variable), it then decodes the response (using dec)
	//note that result is structured data (thanks to dec decoding it)
	//if the llm responded badly it would use the error message and ask it to try again which it would do up to 10 times (line 175)
	result, _, err := mf.Call(context.Background(), CandidateReviewData{
		I:         i,
		Checklist: checklist,
		Resume:    resume,
	})
	if err != nil {
		return nil, err
	}
	//this converts the answers into a diff format (true or false booleans)
	answers := make(map[string]bool)
	for key, resp := range result {
		answers[key] = resp.Answer
	}
	return answers, nil
}

type checklistItemResponse struct {
	Reasoning string `json:"reasoning"`
	Answer    bool   `json:"answer"`
}

//this is sent to jpf which converts to json and sends to chatgpt 
//this is a template
// the {{.I}} bit at the bottom is some irrelevant text (the number of the call) which ensures they are diff each repeat so it isn't just cached
const simpleCandidateReviewTemplate = `You are an expert candidate reviewer. Examine the resume carefully and evaluate every checklist item.

For each checklist entry, produce:
- "reasoning": your full internal reasoning and thought process leading to the answer  
- "answer": true or false

Return a single JSON object where each key matches the exact checklist key.

Checklist:
{{ range $k, $v := .Checklist }}
- {{$k}}: {{$v}}
{{ end }}

Resume:
{{ .Resume }}

{{ .I }}`
