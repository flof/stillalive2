package views

import "strconv"

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
