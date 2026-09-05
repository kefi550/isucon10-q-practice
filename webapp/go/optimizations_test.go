package main

import "testing"

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
