package main

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/jmoiron/sqlx"
)

const (
	chairPublicColumns  = "id, name, description, thumbnail, price, height, width, depth, color, features, kind"
	chairDetailColumns  = chairPublicColumns + ", stock"
	estatePublicColumns = "id, thumbnail, name, description, latitude, longitude, address, rent, door_height, door_width, features"

	chairFeatureTable  = "chair_feature"
	estateFeatureTable = "estate_feature"
)

const maxSearchCacheEntries = 512

type searchResponseCache struct {
	mu               sync.RWMutex
	chairs           map[string]ChairSearchResponse
	estates          map[string]EstateSearchResponse
	chairCounts      map[string]int64
	estateCounts     map[string]int64
	chairGeneration  uint64
	estateGeneration uint64
}

var searchCache = searchResponseCache{
	chairs:       make(map[string]ChairSearchResponse),
	estates:      make(map[string]EstateSearchResponse),
	chairCounts:  make(map[string]int64),
	estateCounts: make(map[string]int64),
}

type chairSearchCall struct {
	done     chan struct{}
	response ChairSearchResponse
	err      error
}

type estateSearchCall struct {
	done     chan struct{}
	response EstateSearchResponse
	err      error
}

type countCall struct {
	done  chan struct{}
	count int64
	err   error
}

type estateListCall struct {
	done    chan struct{}
	estates []Estate
	err     error
}

var searchLoads = struct {
	sync.Mutex
	chairs       map[string]*chairSearchCall
	estates      map[string]*estateSearchCall
	chairCounts  map[string]*countCall
	estateCounts map[string]*countCall
}{
	chairs:       make(map[string]*chairSearchCall),
	estates:      make(map[string]*estateSearchCall),
	chairCounts:  make(map[string]*countCall),
	estateCounts: make(map[string]*countCall),
}

var recommendedLoads = struct {
	sync.Mutex
	calls map[string]*estateListCall
}{calls: make(map[string]*estateListCall)}

func searchLoadKey(generation uint64, key string) string {
	return fmt.Sprintf("%d:%s", generation, key)
}

func loadChairSearchOnce(key string, generation uint64, load func() (ChairSearchResponse, error)) (ChairSearchResponse, error) {
	key = searchLoadKey(generation, key)
	searchLoads.Lock()
	if call, ok := searchLoads.chairs[key]; ok {
		searchLoads.Unlock()
		<-call.done
		return cloneChairSearchResponse(call.response), call.err
	}
	call := &chairSearchCall{done: make(chan struct{})}
	searchLoads.chairs[key] = call
	searchLoads.Unlock()

	call.response, call.err = load()
	searchLoads.Lock()
	delete(searchLoads.chairs, key)
	close(call.done)
	searchLoads.Unlock()
	return cloneChairSearchResponse(call.response), call.err
}

func loadEstateSearchOnce(key string, generation uint64, load func() (EstateSearchResponse, error)) (EstateSearchResponse, error) {
	key = searchLoadKey(generation, key)
	searchLoads.Lock()
	if call, ok := searchLoads.estates[key]; ok {
		searchLoads.Unlock()
		<-call.done
		return cloneEstateSearchResponse(call.response), call.err
	}
	call := &estateSearchCall{done: make(chan struct{})}
	searchLoads.estates[key] = call
	searchLoads.Unlock()

	call.response, call.err = load()
	searchLoads.Lock()
	delete(searchLoads.estates, key)
	close(call.done)
	searchLoads.Unlock()
	return cloneEstateSearchResponse(call.response), call.err
}

func loadCountOnce(calls map[string]*countCall, key string, generation uint64, load func() (int64, error)) (int64, error) {
	key = searchLoadKey(generation, key)
	searchLoads.Lock()
	if call, ok := calls[key]; ok {
		searchLoads.Unlock()
		<-call.done
		return call.count, call.err
	}
	call := &countCall{done: make(chan struct{})}
	calls[key] = call
	searchLoads.Unlock()

	call.count, call.err = load()
	searchLoads.Lock()
	delete(calls, key)
	close(call.done)
	searchLoads.Unlock()
	return call.count, call.err
}

func loadChairCountOnce(key string, generation uint64, load func() (int64, error)) (int64, error) {
	return loadCountOnce(searchLoads.chairCounts, key, generation, load)
}

func loadEstateCountOnce(key string, generation uint64, load func() (int64, error)) (int64, error) {
	return loadCountOnce(searchLoads.estateCounts, key, generation, load)
}

func loadRecommendedEstatesOnce(lo, mid int64, generation uint64, load func() ([]Estate, error)) ([]Estate, error) {
	key := searchLoadKey(generation, fmt.Sprintf("%d:%d", lo, mid))
	recommendedLoads.Lock()
	if call, ok := recommendedLoads.calls[key]; ok {
		recommendedLoads.Unlock()
		<-call.done
		return cloneEstates(call.estates), call.err
	}
	call := &estateListCall{done: make(chan struct{})}
	recommendedLoads.calls[key] = call
	recommendedLoads.Unlock()

	call.estates, call.err = load()
	recommendedLoads.Lock()
	delete(recommendedLoads.calls, key)
	close(call.done)
	recommendedLoads.Unlock()
	return cloneEstates(call.estates), call.err
}

func cloneChairSearchResponse(response ChairSearchResponse) ChairSearchResponse {
	if response.Chairs != nil {
		response.Chairs = append([]Chair{}, response.Chairs...)
	}
	return response
}

func cloneEstateSearchResponse(response EstateSearchResponse) EstateSearchResponse {
	if response.Estates != nil {
		response.Estates = append([]Estate{}, response.Estates...)
	}
	return response
}

func (cache *searchResponseCache) getChair(key string) (ChairSearchResponse, bool) {
	cache.mu.RLock()
	response, ok := cache.chairs[key]
	cache.mu.RUnlock()
	if !ok {
		return ChairSearchResponse{}, false
	}
	return cloneChairSearchResponse(response), true
}

func (cache *searchResponseCache) currentChairGeneration() uint64 {
	cache.mu.RLock()
	generation := cache.chairGeneration
	cache.mu.RUnlock()
	return generation
}

func (cache *searchResponseCache) currentEstateGeneration() uint64 {
	cache.mu.RLock()
	generation := cache.estateGeneration
	cache.mu.RUnlock()
	return generation
}

func (cache *searchResponseCache) putChair(key string, response ChairSearchResponse, generation uint64) {
	cache.mu.Lock()
	if generation != cache.chairGeneration {
		cache.mu.Unlock()
		return
	}
	if _, exists := cache.chairs[key]; !exists && len(cache.chairs) >= maxSearchCacheEntries {
		cache.chairs = make(map[string]ChairSearchResponse)
	}
	cache.chairs[key] = cloneChairSearchResponse(response)
	cache.mu.Unlock()
}

func (cache *searchResponseCache) getChairCount(key string) (int64, bool) {
	cache.mu.RLock()
	count, ok := cache.chairCounts[key]
	cache.mu.RUnlock()
	return count, ok
}

func (cache *searchResponseCache) putChairCount(key string, count int64, generation uint64) {
	cache.mu.Lock()
	if generation == cache.chairGeneration {
		if cache.chairCounts == nil {
			cache.chairCounts = make(map[string]int64)
		}
		if _, exists := cache.chairCounts[key]; !exists && len(cache.chairCounts) >= maxSearchCacheEntries {
			cache.chairCounts = make(map[string]int64)
		}
		cache.chairCounts[key] = count
	}
	cache.mu.Unlock()
}

func (cache *searchResponseCache) getEstate(key string) (EstateSearchResponse, bool) {
	cache.mu.RLock()
	response, ok := cache.estates[key]
	cache.mu.RUnlock()
	if !ok {
		return EstateSearchResponse{}, false
	}
	return cloneEstateSearchResponse(response), true
}

func (cache *searchResponseCache) putEstate(key string, response EstateSearchResponse, generation uint64) {
	cache.mu.Lock()
	if generation != cache.estateGeneration {
		cache.mu.Unlock()
		return
	}
	if _, exists := cache.estates[key]; !exists && len(cache.estates) >= maxSearchCacheEntries {
		cache.estates = make(map[string]EstateSearchResponse)
	}
	cache.estates[key] = cloneEstateSearchResponse(response)
	cache.mu.Unlock()
}

func (cache *searchResponseCache) getEstateCount(key string) (int64, bool) {
	cache.mu.RLock()
	count, ok := cache.estateCounts[key]
	cache.mu.RUnlock()
	return count, ok
}

func (cache *searchResponseCache) putEstateCount(key string, count int64, generation uint64) {
	cache.mu.Lock()
	if generation == cache.estateGeneration {
		if cache.estateCounts == nil {
			cache.estateCounts = make(map[string]int64)
		}
		if _, exists := cache.estateCounts[key]; !exists && len(cache.estateCounts) >= maxSearchCacheEntries {
			cache.estateCounts = make(map[string]int64)
		}
		cache.estateCounts[key] = count
	}
	cache.mu.Unlock()
}

func invalidateChairSearchCache() {
	searchCache.mu.Lock()
	searchCache.chairs = make(map[string]ChairSearchResponse)
	searchCache.chairCounts = make(map[string]int64)
	searchCache.chairGeneration++
	searchCache.mu.Unlock()
}

func invalidateEstateSearchCache() {
	searchCache.mu.Lock()
	searchCache.estates = make(map[string]EstateSearchResponse)
	searchCache.estateCounts = make(map[string]int64)
	searchCache.estateGeneration++
	searchCache.mu.Unlock()
}

func invalidateAllSearchCaches() {
	searchCache.mu.Lock()
	searchCache.chairs = make(map[string]ChairSearchResponse)
	searchCache.estates = make(map[string]EstateSearchResponse)
	searchCache.chairCounts = make(map[string]int64)
	searchCache.estateCounts = make(map[string]int64)
	searchCache.chairGeneration++
	searchCache.estateGeneration++
	searchCache.mu.Unlock()
}

type readResponseCache struct {
	mu sync.RWMutex

	chairs             map[int]Chair
	chairDimensions    map[int][3]int64
	estates            map[int]Estate
	knownEstateIDs     map[int]struct{}
	lowPricedChairs    []Chair
	lowPricedEstates   []Estate
	lowPricedChairSet  bool
	lowPricedEstateSet bool
	recommendedEstates map[[2]int64][]Estate
	chairGeneration    uint64
	estateGeneration   uint64
}

var readCache = readResponseCache{}

func cloneChairs(chairs []Chair) []Chair {
	if chairs == nil {
		return nil
	}
	return append([]Chair{}, chairs...)
}

func cloneEstates(estates []Estate) []Estate {
	if estates == nil {
		return nil
	}
	return append([]Estate{}, estates...)
}

func resetReadCache() {
	readCache.mu.Lock()
	readCache.chairs = make(map[int]Chair)
	readCache.chairDimensions = make(map[int][3]int64)
	readCache.estates = make(map[int]Estate)
	readCache.knownEstateIDs = make(map[int]struct{})
	readCache.lowPricedChairs = nil
	readCache.lowPricedEstates = nil
	readCache.lowPricedChairSet = false
	readCache.lowPricedEstateSet = false
	readCache.recommendedEstates = make(map[[2]int64][]Estate)
	readCache.chairGeneration++
	readCache.estateGeneration++
	readCache.mu.Unlock()
}

func currentChairReadGeneration() uint64 {
	readCache.mu.RLock()
	generation := readCache.chairGeneration
	readCache.mu.RUnlock()
	return generation
}

func currentEstateReadGeneration() uint64 {
	readCache.mu.RLock()
	generation := readCache.estateGeneration
	readCache.mu.RUnlock()
	return generation
}

func getCachedChairDimensions(id int) ([3]int64, bool) {
	readCache.mu.RLock()
	dimensions, ok := readCache.chairDimensions[id]
	readCache.mu.RUnlock()
	return dimensions, ok
}

func rememberChairDimensions(id int, width, height, depth int64) {
	readCache.mu.Lock()
	if readCache.chairDimensions == nil {
		readCache.chairDimensions = make(map[int][3]int64)
	}
	readCache.chairDimensions[id] = [3]int64{width, height, depth}
	readCache.mu.Unlock()
}

func getCachedChair(id int) (Chair, bool) {
	readCache.mu.RLock()
	chair, ok := readCache.chairs[id]
	readCache.mu.RUnlock()
	return chair, ok
}

func rememberChair(chair Chair, generation uint64) {
	readCache.mu.Lock()
	if generation != readCache.chairGeneration {
		readCache.mu.Unlock()
		return
	}
	if readCache.chairs == nil {
		readCache.chairs = make(map[int]Chair)
	}
	readCache.chairs[int(chair.ID)] = chair
	if readCache.chairDimensions == nil {
		readCache.chairDimensions = make(map[int][3]int64)
	}
	readCache.chairDimensions[int(chair.ID)] = [3]int64{chair.Width, chair.Height, chair.Depth}
	readCache.mu.Unlock()
}

func forgetChair(id int) {
	readCache.mu.Lock()
	delete(readCache.chairs, id)
	readCache.lowPricedChairs = nil
	readCache.lowPricedChairSet = false
	readCache.chairGeneration++
	readCache.mu.Unlock()
}

func getCachedEstate(id int) (Estate, bool) {
	readCache.mu.RLock()
	estate, ok := readCache.estates[id]
	readCache.mu.RUnlock()
	return estate, ok
}

func rememberEstate(estate Estate) {
	readCache.mu.Lock()
	if readCache.estates == nil {
		readCache.estates = make(map[int]Estate)
	}
	if readCache.knownEstateIDs == nil {
		readCache.knownEstateIDs = make(map[int]struct{})
	}
	readCache.estates[int(estate.ID)] = estate
	readCache.knownEstateIDs[int(estate.ID)] = struct{}{}
	readCache.mu.Unlock()
}

func rememberEstates(estates []Estate) {
	readCache.mu.Lock()
	if readCache.estates == nil {
		readCache.estates = make(map[int]Estate)
	}
	if readCache.knownEstateIDs == nil {
		readCache.knownEstateIDs = make(map[int]struct{})
	}
	for _, estate := range estates {
		readCache.estates[int(estate.ID)] = estate
		readCache.knownEstateIDs[int(estate.ID)] = struct{}{}
	}
	readCache.mu.Unlock()
}

func isKnownEstate(id int) bool {
	readCache.mu.RLock()
	_, ok := readCache.knownEstateIDs[id]
	readCache.mu.RUnlock()
	return ok
}

// Estate rows are never updated or deleted during a benchmark run. Remembering
// a successful lookup avoids repeating primary-key existence queries.
func rememberEstateID(id int) {
	readCache.mu.Lock()
	if readCache.knownEstateIDs == nil {
		readCache.knownEstateIDs = make(map[int]struct{})
	}
	readCache.knownEstateIDs[id] = struct{}{}
	readCache.mu.Unlock()
}

func getCachedLowPricedChairs() ([]Chair, bool) {
	readCache.mu.RLock()
	chairs, ok := readCache.lowPricedChairs, readCache.lowPricedChairSet
	readCache.mu.RUnlock()
	return cloneChairs(chairs), ok
}

func cacheLowPricedChairs(chairs []Chair, generation uint64) {
	readCache.mu.Lock()
	if generation == readCache.chairGeneration {
		readCache.lowPricedChairs = cloneChairs(chairs)
		readCache.lowPricedChairSet = true
	}
	readCache.mu.Unlock()
}

func invalidateChairReadCaches() {
	readCache.mu.Lock()
	readCache.chairs = make(map[int]Chair)
	readCache.chairDimensions = make(map[int][3]int64)
	readCache.lowPricedChairs = nil
	readCache.lowPricedChairSet = false
	readCache.chairGeneration++
	readCache.mu.Unlock()
}

func getCachedLowPricedEstates() ([]Estate, bool) {
	readCache.mu.RLock()
	estates, ok := readCache.lowPricedEstates, readCache.lowPricedEstateSet
	readCache.mu.RUnlock()
	return cloneEstates(estates), ok
}

func cacheLowPricedEstates(estates []Estate, generation uint64) {
	readCache.mu.Lock()
	if generation == readCache.estateGeneration {
		readCache.lowPricedEstates = cloneEstates(estates)
		readCache.lowPricedEstateSet = true
	}
	readCache.mu.Unlock()
}

func getCachedRecommendedEstates(lo, mid int64) ([]Estate, bool) {
	readCache.mu.RLock()
	estates, ok := readCache.recommendedEstates[[2]int64{lo, mid}]
	readCache.mu.RUnlock()
	return cloneEstates(estates), ok
}

func cacheRecommendedEstates(lo, mid int64, estates []Estate, generation uint64) {
	readCache.mu.Lock()
	if generation == readCache.estateGeneration {
		if readCache.recommendedEstates == nil {
			readCache.recommendedEstates = make(map[[2]int64][]Estate)
		}
		key := [2]int64{lo, mid}
		if _, exists := readCache.recommendedEstates[key]; !exists && len(readCache.recommendedEstates) >= maxSearchCacheEntries {
			readCache.recommendedEstates = make(map[[2]int64][]Estate)
		}
		readCache.recommendedEstates[key] = cloneEstates(estates)
	}
	readCache.mu.Unlock()
}

func invalidateEstateReadCaches() {
	readCache.mu.Lock()
	readCache.estates = make(map[int]Estate)
	readCache.knownEstateIDs = make(map[int]struct{})
	readCache.lowPricedEstates = nil
	readCache.lowPricedEstateSet = false
	readCache.recommendedEstates = make(map[[2]int64][]Estate)
	readCache.estateGeneration++
	readCache.mu.Unlock()
}

// dbForKind は "chair"/"estate" のどちらの feature index / prepared statement を
// 扱っているかに応じて、対応する分割先の *sqlx.DB を返す。
func dbForKind(kind string) *sqlx.DB {
	if kind == "chair" {
		return chairDB
	}
	return estateDB
}

type featureIndexState struct {
	mu sync.Mutex

	chairReady        bool
	estateReady       bool
	chairUnavailable  bool
	estateUnavailable bool
}

var featureIndexes featureIndexState

func resetFeatureIndexState() {
	featureIndexes.mu.Lock()
	featureIndexes.chairReady = false
	featureIndexes.estateReady = false
	featureIndexes.chairUnavailable = false
	featureIndexes.estateUnavailable = false
	featureIndexes.mu.Unlock()
}

func markFeatureIndexReady(kind string) {
	featureIndexes.mu.Lock()
	if kind == "chair" {
		featureIndexes.chairReady = true
		featureIndexes.chairUnavailable = false
	} else {
		featureIndexes.estateReady = true
		featureIndexes.estateUnavailable = false
	}
	featureIndexes.mu.Unlock()
}

func featureIndexSpec(kind string) (baseTable, indexTable, idColumn string, features []string) {
	if kind == "chair" {
		return "chair", chairFeatureTable, "chair_id", chairSearchCondition.Feature.List
	}
	return "estate", estateFeatureTable, "estate_id", estateSearchCondition.Feature.List
}

func uniqueFeatureValues(features []string) []string {
	values := make([]string, 0, len(features))
	seen := make(map[string]struct{}, len(features))
	for _, feature := range features {
		if _, ok := seen[feature]; ok {
			continue
		}
		seen[feature] = struct{}{}
		values = append(values, feature)
	}
	return values
}

func isFeatureIndexReady(kind string) bool {
	featureIndexes.mu.Lock()
	defer featureIndexes.mu.Unlock()

	if kind == "chair" {
		return featureIndexes.chairReady
	}
	return featureIndexes.estateReady
}

func appendFeatureIndexRows(rows [][]interface{}, id int, rawFeatures string, features []string) [][]interface{} {
	for _, feature := range features {
		if strings.Contains(rawFeatures, feature) {
			rows = append(rows, []interface{}{id, feature})
		}
	}
	return rows
}

func insertFeatureIndexBatch(tx *sql.Tx, kind string, rows [][]interface{}) error {
	_, indexTable, idColumn, _ := featureIndexSpec(kind)
	return insertBatch(tx, indexTable, []string{idColumn, "feature_value"}, rows)
}

func rebuildFeatureIndexTx(tx *sql.Tx, baseTable, indexTable, idColumn string, features []string) error {
	if _, err := tx.Exec("DELETE FROM " + indexTable); err != nil {
		return err
	}

	values := uniqueFeatureValues(features)
	if len(values) == 0 {
		return nil
	}

	featureSelects := make([]string, len(values))
	args := make([]interface{}, len(values))
	for i, feature := range values {
		if i == 0 {
			featureSelects[i] = "SELECT ? AS feature_value"
		} else {
			featureSelects[i] = "SELECT ?"
		}
		args[i] = feature
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s, feature_value) "+
			"SELECT base_row.id, feature_values.feature_value "+
			"FROM %s AS base_row "+
			"JOIN (%s) AS feature_values "+
			"ON base_row.features LIKE CONCAT('%%', feature_values.feature_value, '%%')",
		indexTable,
		idColumn,
		baseTable,
		strings.Join(featureSelects, " UNION ALL "),
	)
	_, err := tx.Exec(query, args...)
	return err
}

func rebuildFeatureIndexTxForKind(tx *sql.Tx, kind string) error {
	baseTable, indexTable, idColumn, features := featureIndexSpec(kind)
	return rebuildFeatureIndexTx(tx, baseTable, indexTable, idColumn, features)
}

// rebuildFeatureIndexForKind は指定した kind ("chair" または "estate") の feature index を
// その kind が実際に置かれている DB (chairDB / estateDB) 上のトランザクションで再構築する。
// chair と estate は別インスタンスの MySQL に分かれているため、1つのトランザクションに
// まとめることはできない。
func rebuildFeatureIndexForKind(kind string) error {
	tx, err := dbForKind(kind).Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := rebuildFeatureIndexTxForKind(tx, kind); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	markFeatureIndexReady(kind)
	return nil
}

func rebuildFeatureIndexes() error {
	if err := rebuildFeatureIndexForKind("chair"); err != nil {
		return err
	}
	if err := rebuildFeatureIndexForKind("estate"); err != nil {
		return err
	}
	return nil
}

func isConfiguredFeature(feature string, configured []string) bool {
	// The original LIKE query treats these characters as wildcards. Do not
	// replace such queries with equality against the normalized index.
	if strings.ContainsAny(feature, "%_\\") {
		return false
	}
	for _, candidate := range configured {
		if candidate == feature {
			return true
		}
	}
	return false
}

func hasConfiguredFeatureQuery(query string, configured []string) bool {
	for _, feature := range strings.Split(query, ",") {
		if isConfiguredFeature(feature, configured) {
			return true
		}
	}
	return false
}

func ensureFeatureIndex(kind string) bool {
	featureIndexes.mu.Lock()
	defer featureIndexes.mu.Unlock()

	ready, unavailable := false, false
	if kind == "chair" {
		ready = featureIndexes.chairReady
		unavailable = featureIndexes.chairUnavailable
	} else {
		ready = featureIndexes.estateReady
		unavailable = featureIndexes.estateUnavailable
	}
	if ready {
		return true
	}
	targetDB := dbForKind(kind)
	if unavailable || targetDB == nil {
		return false
	}

	tx, err := targetDB.Begin()
	if err == nil {
		err = rebuildFeatureIndexTxForKind(tx, kind)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil {
		if kind == "chair" {
			featureIndexes.chairUnavailable = true
		} else {
			featureIndexes.estateUnavailable = true
		}
		return false
	}

	if kind == "chair" {
		featureIndexes.chairReady = true
	} else {
		featureIndexes.estateReady = true
	}
	return true
}

func ensureChairFeatureIndex() bool {
	return ensureFeatureIndex("chair")
}

func ensureEstateFeatureIndex() bool {
	return ensureFeatureIndex("estate")
}

// chair 用と estate 用の prepared statement は、それぞれ chairDB / estateDB という
// 別インスタンスの MySQL に対して個別に準備する必要がある。

type chairPreparedQuerySet struct {
	chairDetail      *sqlx.Stmt
	buyChair         *sqlx.Stmt
	lowPricedChair   *sqlx.Stmt
	recommendedChair *sqlx.Stmt
}

func (queries *chairPreparedQuerySet) close() {
	if queries == nil {
		return
	}
	for _, stmt := range []*sqlx.Stmt{queries.chairDetail, queries.buyChair, queries.lowPricedChair, queries.recommendedChair} {
		if stmt != nil {
			_ = stmt.Close()
		}
	}
}

type estatePreparedQuerySet struct {
	estateDetail      *sqlx.Stmt
	lowPricedEstate   *sqlx.Stmt
	recommendedEstate *sqlx.Stmt
	estateExists      *sqlx.Stmt
	nazotteEstate     *sqlx.Stmt
}

func (queries *estatePreparedQuerySet) close() {
	if queries == nil {
		return
	}
	for _, stmt := range []*sqlx.Stmt{queries.estateDetail, queries.lowPricedEstate, queries.recommendedEstate, queries.estateExists, queries.nazotteEstate} {
		if stmt != nil {
			_ = stmt.Close()
		}
	}
}

var chairPreparedQueryState struct {
	sync.RWMutex
	queries     *chairPreparedQuerySet
	unavailable bool
}

var estatePreparedQueryState struct {
	sync.RWMutex
	queries     *estatePreparedQuerySet
	unavailable bool
}

func prepareChairQuerySet() (*chairPreparedQuerySet, error) {
	queries := &chairPreparedQuerySet{}
	var err error
	prepare := func(destination **sqlx.Stmt, query string) error {
		*destination, err = chairDB.Preparex(query)
		return err
	}

	if err := prepare(&queries.chairDetail, "SELECT "+chairDetailColumns+" FROM chair WHERE id = ?"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.buyChair, "UPDATE chair SET stock = LAST_INSERT_ID(stock - 1) WHERE id = ? AND stock > 0"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.lowPricedChair, "SELECT "+chairPublicColumns+" FROM chair WHERE stock > 0 ORDER BY price ASC, id ASC LIMIT ?"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.recommendedChair, "SELECT width, height, depth FROM chair WHERE id = ?"); err != nil {
		queries.close()
		return nil, err
	}
	return queries, nil
}

func prepareEstateQuerySet() (*estatePreparedQuerySet, error) {
	queries := &estatePreparedQuerySet{}
	var err error
	prepare := func(destination **sqlx.Stmt, query string) error {
		*destination, err = estateDB.Preparex(query)
		return err
	}

	if err := prepare(&queries.estateDetail, "SELECT "+estatePublicColumns+" FROM estate WHERE id = ?"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.lowPricedEstate, "SELECT "+estatePublicColumns+" FROM estate ORDER BY rent ASC, id ASC LIMIT ?"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.recommendedEstate, "SELECT "+estatePublicColumns+" FROM estate WHERE (door_width >= ? AND door_height >= ?) OR (door_width >= ? AND door_height >= ?) ORDER BY popularity_desc ASC, id ASC LIMIT ?"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.estateExists, "SELECT 1 FROM estate WHERE id = ? LIMIT 1"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.nazotteEstate, "SELECT "+estatePublicColumns+" FROM estate WHERE latitude <= ? AND latitude >= ? AND longitude <= ? AND longitude >= ? AND ST_Contains(ST_PolygonFromText(?), Point(latitude, longitude)) ORDER BY popularity_desc ASC, id ASC LIMIT ?"); err != nil {
		queries.close()
		return nil, err
	}
	return queries, nil
}

func chairPreparedQueriesOrNil() *chairPreparedQuerySet {
	chairPreparedQueryState.RLock()
	queries := chairPreparedQueryState.queries
	unavailable := chairPreparedQueryState.unavailable
	chairPreparedQueryState.RUnlock()
	if queries != nil || unavailable || chairDB == nil {
		return queries
	}

	chairPreparedQueryState.Lock()
	defer chairPreparedQueryState.Unlock()
	if chairPreparedQueryState.queries != nil || chairPreparedQueryState.unavailable || chairDB == nil {
		return chairPreparedQueryState.queries
	}

	queries, err := prepareChairQuerySet()
	if err != nil {
		chairPreparedQueryState.unavailable = true
		return nil
	}
	chairPreparedQueryState.queries = queries
	return queries
}

func estatePreparedQueriesOrNil() *estatePreparedQuerySet {
	estatePreparedQueryState.RLock()
	queries := estatePreparedQueryState.queries
	unavailable := estatePreparedQueryState.unavailable
	estatePreparedQueryState.RUnlock()
	if queries != nil || unavailable || estateDB == nil {
		return queries
	}

	estatePreparedQueryState.Lock()
	defer estatePreparedQueryState.Unlock()
	if estatePreparedQueryState.queries != nil || estatePreparedQueryState.unavailable || estateDB == nil {
		return estatePreparedQueryState.queries
	}

	queries, err := prepareEstateQuerySet()
	if err != nil {
		estatePreparedQueryState.unavailable = true
		return nil
	}
	estatePreparedQueryState.queries = queries
	return queries
}

func invalidateChairPreparedQueries() {
	chairPreparedQueryState.Lock()
	queries := chairPreparedQueryState.queries
	chairPreparedQueryState.queries = nil
	chairPreparedQueryState.unavailable = false
	chairPreparedQueryState.Unlock()
	queries.close()
}

func invalidateEstatePreparedQueries() {
	estatePreparedQueryState.Lock()
	queries := estatePreparedQueryState.queries
	estatePreparedQueryState.queries = nil
	estatePreparedQueryState.unavailable = false
	estatePreparedQueryState.Unlock()
	queries.close()
}

// reopenDBPool は estateDB / chairDB の両方の接続プールを張り直す。
// /initialize でスキーマを作り直した後、古いコネクションが不整合な状態を
// キャッシュし続けないようにするために呼ぶ。
func reopenDBPool() error {
	nextEstate, err := estateMySQLConnectionData.ConnectDB()
	if err != nil {
		return err
	}
	configureDBPool(nextEstate)
	oldEstate := estateDB
	estateDB = nextEstate
	if oldEstate != nil {
		_ = oldEstate.Close()
	}

	nextChair, err := chairMySQLConnectionData.ConnectDB()
	if err != nil {
		return err
	}
	configureDBPool(nextChair)
	oldChair := chairDB
	chairDB = nextChair
	if oldChair != nil {
		_ = oldChair.Close()
	}
	return nil
}
