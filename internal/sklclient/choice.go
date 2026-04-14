package sklclient

import "strings"

var choices = []string{"A", "B", "C", "D"}

func IndexToChoice(i int) string {
	if i < 0 || i >= len(choices) {
		return ""
	}
	return choices[i]
}

func ChoiceToIndex(s string) (int, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "A":
		return 0, true
	case "B":
		return 1, true
	case "C":
		return 2, true
	case "D":
		return 3, true
	default:
		return 0, false
	}
}

