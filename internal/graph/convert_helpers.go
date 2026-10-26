package graph

// convertRows applies a typed GraphQL conversion without changing row order.
// It returns a non-nil empty slice when rows is nil or empty.
func convertRows[Row, Out any](rows []Row, convert func(Row) *Out) []*Out {
	out := make([]*Out, len(rows))
	for i, row := range rows {
		out[i] = convert(row)
	}
	return out
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
