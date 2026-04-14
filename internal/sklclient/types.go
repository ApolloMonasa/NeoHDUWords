package sklclient

import "time"

type PaperSummary struct {
	PaperID   string     `json:"paperId"`
	Type      string     `json:"type"`
	Week      int        `json:"week"`
	StartTime *time.Time `json:"startTime"`
	EndTime   *time.Time `json:"endTime"`
	Mark      int        `json:"mark"`
}

type Paper struct {
	PaperID string `json:"paperId"`
	Week    int    `json:"week"`
}

type PaperDetail struct {
	PaperID  string     `json:"paperId"`
	Type     string     `json:"type"`
	Week     int        `json:"week"`
	StartTime *time.Time `json:"startTime"`
	EndTime  *time.Time `json:"endTime"`
	Mark     int        `json:"mark"`
	List     []Question `json:"list"`
}

type Question struct {
	PaperDetailID string `json:"paperDetailId"`
	Title         string `json:"title"`
	AnswerA       string `json:"answerA"`
	AnswerB       string `json:"answerB"`
	AnswerC       string `json:"answerC"`
	AnswerD       string `json:"answerD"`
	QuestionNum   int    `json:"questionNum"`
	Answer        string `json:"answer"`
	Input         string `json:"input"`
	Right         *bool  `json:"right"`
}

func (q Question) Options() []string {
	return []string{q.AnswerA, q.AnswerB, q.AnswerC, q.AnswerD}
}

type SaveInput struct {
	Question
}

