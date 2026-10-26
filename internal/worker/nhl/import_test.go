package nhl

import (
	"reflect"
	"slices"
	"testing"

	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
)

// unwiredQueryFields names the interface fields of the struct activities
// points to that do not hold queries, skipping the non-query dependencies
// listed in others. A query field added to an activity struct but not to its
// constructor shows up here.
func unwiredQueryFields(activities, queries any, others ...string) []string {
	v := reflect.ValueOf(activities).Elem()
	var unwired []string
	for i := range v.NumField() {
		field := v.Type().Field(i)
		if field.Type.Kind() != reflect.Interface || slices.Contains(others, field.Name) {
			continue
		}
		if v.Field(i).Interface() != queries {
			unwired = append(unwired, field.Name)
		}
	}
	return unwired
}

func TestNewImportActivitiesWiresEveryQueryField(t *testing.T) {
	queries := struct{ ImportActivityQueries }{}
	mem := store.NewMemStorage()
	tx := &stubTransactor{}
	yahooActivities := &yahoo.ImportActivities{}

	got := NewImportActivities(queries, ImportActivities{Storage: mem, Tx: tx, Yahoo: yahooActivities})

	assert.Empty(t, unwiredQueryFields(got, queries, "Storage", "Tx"))
	assert.Same(t, mem, got.Storage)
	assert.Same(t, tx, got.Tx)
	assert.Same(t, yahooActivities, got.Yahoo)
}
