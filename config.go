package main

import (
	"encoding/json"
	"errors"
	"os"
)

type ConfigScoreChecklistItem struct {
	Question  string
	Weight    float64
	Important bool
}

type configScoreChecklistItemDTO struct {
	Question  string   `json:"question"`
	Weight    *float64 `json:"weight,omitempty"`
	Important bool     `json:"important,omitempty"`
}

func (c *ConfigScoreChecklistItem) UnmarshalJSON(data []byte) error {
	var dto configScoreChecklistItemDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	if dto.Weight == nil {
		c.Weight = 1
	} else {
		c.Weight = *dto.Weight
	}
	c.Question = dto.Question
	c.Important = dto.Important

	return nil
}

func (c *ConfigScoreChecklistItem) MarshalJSON() ([]byte, error) {
	var w *float64
	//sets default weight as 1
	if c.Weight != 1 {
		w = &c.Weight
	}
	dto := configScoreChecklistItemDTO{
		Question:  c.Question,
		Weight:    w,
		Important: c.Important,
	}
	return json.Marshal(dto)
}

type ConfigView struct {
	PrettyName     string                              `json:"pretty_name"`
	ScoreChecklist map[string]ConfigScoreChecklistItem `json:"score_checklist"`
}

//structs are equiv of pythoin classes
//views and apikey map to the json code
type Config struct {
	//viwes is a dictionary with string type keys and values of configview(which is another class)
	Views  map[string]ConfigView `json:"views"`
	//api key is a string and the json bit states when we convert to and from json as APIKey and api_key
	APIKey string                `json:"api_key"`
}

//this returns a tuple and an error if it goes wrong (in Go errors are variable types)
func LoadConfig() (Config, error) {
	f, err := os.Open("./config.json")
	if err != nil {
		return Config{}, errors.Join(errors.New("failed to read config file"), err)
	}
	defer f.Close()
	cfg := Config{}
	err = json.NewDecoder(f).Decode(&cfg)
	if err != nil {
		return Config{}, errors.Join(errors.New("failed to parse config fike"), err)
	}
	return cfg, nil
}
