package main

import (
	"fmt"
	"reflect"
	"testing"
)

func rangeCondition(ranges []Range) RangeCondition {
	condition := RangeCondition{Ranges: make([]*Range, len(ranges))}
	for i := range ranges {
		rangeCopy := ranges[i]
		condition.Ranges[i] = &rangeCopy
	}
	return condition
}

func TestAppendBucketedRangeSearchConditionUsesConfiguredBucket(t *testing.T) {
	condition := rangeCondition(estateDoorRanges)
	conditions, params := appendBucketedRangeSearchCondition(
		[]string{"existing = ?"}, []interface{}{1}, condition, condition.Ranges[2],
		estateDoorRanges, "door_height", "door_height_range_id",
	)

	if want := []string{"existing = ?", "door_height_range_id = ?"}; !reflect.DeepEqual(conditions, want) {
		t.Fatalf("unexpected conditions: %#v", conditions)
	}
	if want := []interface{}{1, int64(2)}; !reflect.DeepEqual(params, want) {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestAppendBucketedRangeSearchConditionFallsBackForDifferentFixture(t *testing.T) {
	condition := rangeCondition(estateRentRanges)
	condition.Ranges[1].Max = 99999
	selected := &Range{ID: 7, Min: 50000, Max: 99999}
	conditions, params := appendBucketedRangeSearchCondition(
		nil, nil, condition, selected,
		estateRentRanges, "rent", "rent_range_id",
	)

	if want := []string{"rent >= ?", "rent < ?"}; !reflect.DeepEqual(conditions, want) {
		t.Fatalf("unexpected conditions: %#v", conditions)
	}
	if want := []interface{}{int64(50000), int64(99999)}; !reflect.DeepEqual(params, want) {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestAppendBucketedRangeSearchConditionUsesChairPriceBucket(t *testing.T) {
	condition := rangeCondition(chairPriceRanges)
	conditions, params := appendBucketedRangeSearchCondition(
		nil, nil, condition, condition.Ranges[4],
		chairPriceRanges, "price", "price_range_id",
	)

	if want := []string{"price_range_id = ?"}; !reflect.DeepEqual(conditions, want) {
		t.Fatalf("unexpected conditions: %#v", conditions)
	}
	if want := []interface{}{int64(4)}; !reflect.DeepEqual(params, want) {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestRangeConditionMatchesRejectsNilRange(t *testing.T) {
	condition := rangeCondition(estateDoorRanges)
	condition.Ranges[0] = nil
	if rangeConditionMatches(condition, estateDoorRanges) {
		t.Fatal("range condition with a nil range matched generated buckets")
	}
}

func TestAppendFeatureSearchConditionsAggregatesConfiguredFeatures(t *testing.T) {
	conditions, params := appendFeatureSearchConditions(
		[]string{"price >= ?"},
		[]interface{}{3000},
		"mesh,modern,mesh",
		[]string{"mesh", "modern"},
		true,
		"chair",
		"chair_feature",
		"chair_id",
	)

	wantConditions := []string{
		"price >= ?",
		"chair.id IN (SELECT feature_match.chair_id FROM chair_feature AS feature_match WHERE feature_match.feature_value IN (?,?) GROUP BY feature_match.chair_id HAVING COUNT(*) = ?)",
	}
	wantParams := []interface{}{3000, "mesh", "modern", 2}
	if !reflect.DeepEqual(conditions, wantConditions) {
		t.Fatalf("unexpected conditions: %#v", conditions)
	}
	if !reflect.DeepEqual(params, wantParams) {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestAppendFeatureSearchConditionsPreservesLikeFallback(t *testing.T) {
	conditions, params := appendFeatureSearchConditions(
		nil,
		nil,
		"configured,unknown%,configured_",
		[]string{"configured", "configured_"},
		true,
		"estate",
		"estate_feature",
		"estate_id",
	)

	wantConditions := []string{
		"estate.features LIKE CONCAT('%', ?, '%')",
		"estate.features LIKE CONCAT('%', ?, '%')",
		"estate.id IN (SELECT feature_match.estate_id FROM estate_feature AS feature_match WHERE feature_match.feature_value IN (?) GROUP BY feature_match.estate_id HAVING COUNT(*) = ?)",
	}
	wantParams := []interface{}{"unknown%", "configured_", "configured", 1}
	if !reflect.DeepEqual(conditions, wantConditions) {
		t.Fatalf("unexpected conditions: %#v", conditions)
	}
	if !reflect.DeepEqual(params, wantParams) {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestAppendFeatureSearchConditionsWithoutIndexUsesOriginalLikeMatching(t *testing.T) {
	conditions, params := appendFeatureSearchConditions(
		nil,
		nil,
		"mesh,modern",
		[]string{"mesh", "modern"},
		false,
		"chair",
		"chair_feature",
		"chair_id",
	)

	wantConditions := []string{
		"chair.features LIKE CONCAT('%', ?, '%')",
		"chair.features LIKE CONCAT('%', ?, '%')",
	}
	wantParams := []interface{}{"mesh", "modern"}
	if !reflect.DeepEqual(conditions, wantConditions) {
		t.Fatalf("unexpected conditions: %#v", conditions)
	}
	if !reflect.DeepEqual(params, wantParams) {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestCloneResponsePreservesEmptyJSONArrays(t *testing.T) {
	chairResponse := cloneChairSearchResponse(ChairSearchResponse{Chairs: []Chair{}})
	if chairResponse.Chairs == nil || !reflect.DeepEqual(chairResponse.Chairs, []Chair{}) {
		t.Fatal("chair response clone changed an empty array to null")
	}

	estateResponse := cloneEstateSearchResponse(EstateSearchResponse{Estates: []Estate{}})
	if estateResponse.Estates == nil || !reflect.DeepEqual(estateResponse.Estates, []Estate{}) {
		t.Fatal("estate response clone changed an empty array to null")
	}
}

func TestChairInvalidationDoesNotRejectEstateCacheWrite(t *testing.T) {
	searchCache = searchResponseCache{
		chairs:  make(map[string]ChairSearchResponse),
		estates: make(map[string]EstateSearchResponse),
	}

	generation := searchCache.currentEstateGeneration()
	invalidateChairSearchCache()
	searchCache.putEstate("estate", EstateSearchResponse{Count: 1}, generation)

	response, ok := searchCache.getEstate("estate")
	if !ok || response.Count != 1 {
		t.Fatal("chair invalidation rejected an unrelated estate cache write")
	}
}

func TestEstateInvalidationDoesNotRejectChairCacheWrite(t *testing.T) {
	searchCache = searchResponseCache{
		chairs:  make(map[string]ChairSearchResponse),
		estates: make(map[string]EstateSearchResponse),
	}

	generation := searchCache.currentChairGeneration()
	invalidateEstateSearchCache()
	searchCache.putChair("chair", ChairSearchResponse{Count: 1}, generation)

	response, ok := searchCache.getChair("chair")
	if !ok || response.Count != 1 {
		t.Fatal("estate invalidation rejected an unrelated chair cache write")
	}
}

func TestSearchCountCacheIsSharedAcrossPagesAndInvalidated(t *testing.T) {
	searchCache = searchResponseCache{
		chairs:       make(map[string]ChairSearchResponse),
		estates:      make(map[string]EstateSearchResponse),
		chairCounts:  make(map[string]int64),
		estateCounts: make(map[string]int64),
	}

	generation := searchCache.currentEstateGeneration()
	searchCache.putEstateCount("rentRangeId=1", 123, generation)
	if count, ok := searchCache.getEstateCount("rentRangeId=1"); !ok || count != 123 {
		t.Fatalf("unexpected cached count: count=%d ok=%v", count, ok)
	}

	invalidateEstateSearchCache()
	if _, ok := searchCache.getEstateCount("rentRangeId=1"); ok {
		t.Fatal("estate count survived estate invalidation")
	}
}

func TestChairSearchCachesEvictSingleEntryAtCapacity(t *testing.T) {
	searchCache = searchResponseCache{
		chairs:      make(map[string]ChairSearchResponse),
		chairCounts: make(map[string]int64),
	}
	generation := searchCache.currentChairGeneration()

	for i := 0; i < maxChairSearchResponseCacheEntries; i++ {
		searchCache.putChair(fmt.Sprintf("response-%d", i), ChairSearchResponse{Count: int64(i)}, generation)
	}
	searchCache.putChair("response-overflow", ChairSearchResponse{Count: 1}, generation)
	if got := len(searchCache.chairs); got != maxChairSearchResponseCacheEntries {
		t.Fatalf("unexpected chair response cache size after eviction: %d", got)
	}
	if _, ok := searchCache.getChair("response-overflow"); !ok {
		t.Fatal("new chair response was not cached after eviction")
	}

	for i := 0; i < maxChairSearchCountCacheEntries; i++ {
		searchCache.putChairCount(fmt.Sprintf("count-%d", i), int64(i), generation)
	}
	searchCache.putChairCount("count-overflow", 1, generation)
	if got := len(searchCache.chairCounts); got != maxChairSearchCountCacheEntries {
		t.Fatalf("unexpected chair count cache size after eviction: %d", got)
	}
	if _, ok := searchCache.getChairCount("count-overflow"); !ok {
		t.Fatal("new chair count was not cached after eviction")
	}
}

func TestStaleReadResultsAreNotCachedAfterInvalidation(t *testing.T) {
	resetReadCache()
	estateGeneration := currentEstateReadGeneration()
	chairGeneration := currentChairReadGeneration()

	invalidateEstateReadCaches()
	cacheLowPricedEstates([]Estate{{ID: 1}}, estateGeneration)
	cacheRecommendedEstates(70, 100, []Estate{{ID: 1}}, estateGeneration)
	if _, ok := getCachedLowPricedEstates(); ok {
		t.Fatal("stale low-priced estates were cached")
	}
	if _, ok := getCachedRecommendedEstates(70, 100); ok {
		t.Fatal("stale recommended estates were cached")
	}

	invalidateChairReadCaches()
	cacheLowPricedChairs([]Chair{{ID: 1}}, chairGeneration)
	rememberChair(Chair{ID: 1, Stock: 1}, chairGeneration)
	if _, ok := getCachedLowPricedChairs(); ok {
		t.Fatal("stale low-priced chairs were cached")
	}
	if _, ok := getCachedChair(1); ok {
		t.Fatal("stale chair detail was cached")
	}
}
