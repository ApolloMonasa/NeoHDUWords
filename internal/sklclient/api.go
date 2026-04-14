package sklclient

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

type coded struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (c coded) APIErrorFields() (int, string) { return c.Code, c.Msg }

func (c *Client) UserInfo(ctx context.Context, userType int) (map[string]any, error) {
	q := url.Values{}
	q.Set("type", strconv.Itoa(userType))
	q.Set("index", "")
	var out map[string]any
	if err := c.do(ctx, "GET", "/api/userinfo", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) PaperList(ctx context.Context, paperType int) ([]PaperSummary, error) {
	q := url.Values{}
	q.Set("type", strconv.Itoa(paperType))
	q.Set("week", "0")
	q.Set("schoolYear", "")
	q.Set("semester", "")
	var out []PaperSummary
	if err := c.do(ctx, "GET", "/api/paper/list", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type paperNewResp struct {
	coded
	PaperDetail
}

func (c *Client) PaperNew(ctx context.Context, paperType int, week int, startTimeMS int64) (PaperDetail, error) {
	q := url.Values{}
	q.Set("type", strconv.Itoa(paperType))
	q.Set("week", strconv.Itoa(week))
	q.Set("startTime", strconv.FormatInt(startTimeMS, 10))

	var out paperNewResp
	if err := c.do(ctx, "GET", "/api/paper/new", q, nil, &out); err != nil {
		return PaperDetail{}, err
	}
	return out.PaperDetail, nil
}

func (c *Client) PaperDetail(ctx context.Context, paperID string) (PaperDetail, error) {
	q := url.Values{}
	q.Set("paperId", paperID)
	var out PaperDetail
	if err := c.do(ctx, "GET", "/api/paper/detail", q, nil, &out); err != nil {
		return PaperDetail{}, err
	}
	return out, nil
}

type paperSaveReq struct {
	PaperID string     `json:"paperId"`
	List    []Question `json:"list"`
}

func (c *Client) PaperSave(ctx context.Context, paperID string, list []Question) error {
	req := paperSaveReq{
		PaperID: paperID,
		List:    list,
	}
	q := url.Values{}
	return c.do(ctx, "POST", "/api/paper/save", q, req, nil)
}

func (c *Client) PaperSubmit(ctx context.Context, paperID string) error {
	req := paperSaveReq{
		PaperID: paperID,
		List:    []Question{},
	}
	q := url.Values{}
	return c.do(ctx, "POST", "/api/paper/save", q, req, nil)
}

func (c *Client) GetOrCreateActivePaper(ctx context.Context, paperType int) (Paper, error) {
	list, err := c.PaperList(ctx, paperType)
	if err != nil {
		return Paper{}, err
	}

	var (
		activePaperID string
		activeWeek    int
		activeStart   time.Time
		hasActive     bool
		maxWeek       int
	)
	for _, p := range list {
		if p.Week > maxWeek {
			maxWeek = p.Week
		}
		if p.EndTime == nil {
			if !hasActive {
				activePaperID = p.PaperID
				activeWeek = p.Week
				if p.StartTime != nil {
					activeStart = *p.StartTime
				}
				hasActive = true
				continue
			}

			if p.StartTime != nil {
				if activeStart.IsZero() || p.StartTime.After(activeStart) {
					activePaperID = p.PaperID
					activeWeek = p.Week
					activeStart = *p.StartTime
				}
			}
		}
	}
	if activePaperID != "" {
		return Paper{PaperID: activePaperID, Week: activeWeek}, nil
	}

	week := maxWeek
	startMS := time.Now().UnixMilli()
	newPaper, err := c.PaperNew(ctx, paperType, week, startMS)
	if err != nil {
		return Paper{}, err
	}
	if newPaper.PaperID == "" {
		return Paper{}, fmt.Errorf("paper/new returned empty paperId")
	}
	return Paper{PaperID: newPaper.PaperID, Week: newPaper.Week}, nil
}

func (c *Client) CreateFreshPaper(ctx context.Context, paperType int) (Paper, error) {
	list, err := c.PaperList(ctx, paperType)
	if err != nil {
		return Paper{}, err
	}

	maxWeek := 0
	for _, p := range list {
		if p.Week > maxWeek {
			maxWeek = p.Week
		}
	}

	startMS := time.Now().UnixMilli()
	newPaper, err := c.PaperNew(ctx, paperType, maxWeek, startMS)
	if err != nil {
		return Paper{}, err
	}
	if newPaper.PaperID == "" {
		return Paper{}, fmt.Errorf("paper/new returned empty paperId")
	}
	return Paper{PaperID: newPaper.PaperID, Week: newPaper.Week}, nil
}

func (c *Client) CreateExamPaper(ctx context.Context, paperType int) (Paper, error) {
	startMS := time.Now().UnixMilli()
	newPaper, err := c.PaperNew(ctx, paperType, 0, startMS)
	if err != nil {
		return Paper{}, err
	}
	if newPaper.PaperID == "" {
		return Paper{}, fmt.Errorf("paper/new returned empty paperId")
	}
	return Paper{PaperID: newPaper.PaperID, Week: newPaper.Week}, nil
}
