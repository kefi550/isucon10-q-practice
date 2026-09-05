package main

import (
	"reflect"
	"testing"
)

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
