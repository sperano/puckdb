package date

import "time"

const readableShortTSFormat = "2006-1-2"

func ToYearString(time time.Time) string {
	return time.Format(readableShortTSFormat)
}
