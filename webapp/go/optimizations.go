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
	chairGeneration  uint64
	estateGeneration uint64
}

var searchCache = searchResponseCache{
	chairs:  make(map[string]ChairSearchResponse),
	estates: make(map[string]EstateSearchResponse),
}

func cloneChairSearchResponse(response ChairSearchResponse) ChairSearchResponse {
	response.Chairs = append([]Chair(nil), response.Chairs...)
	return response
}

func cloneEstateSearchResponse(response EstateSearchResponse) EstateSearchResponse {
	response.Estates = append([]Estate(nil), response.Estates...)
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

func invalidateChairSearchCache() {
	searchCache.mu.Lock()
	searchCache.chairs = make(map[string]ChairSearchResponse)
	searchCache.chairGeneration++
	searchCache.mu.Unlock()
}

func invalidateEstateSearchCache() {
	searchCache.mu.Lock()
	searchCache.estates = make(map[string]EstateSearchResponse)
	searchCache.estateGeneration++
	searchCache.mu.Unlock()
}

func invalidateAllSearchCaches() {
	searchCache.mu.Lock()
	searchCache.chairs = make(map[string]ChairSearchResponse)
	searchCache.estates = make(map[string]EstateSearchResponse)
	searchCache.chairGeneration++
	searchCache.estateGeneration++
	searchCache.mu.Unlock()
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
	if err := prepare(&queries.buyChair, "UPDATE chair SET stock = stock - 1 WHERE id = ? AND stock > 0"); err != nil {
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
	if err := prepare(&queries.recommendedEstate, "SELECT "+estatePublicColumns+" FROM estate WHERE (door_width >= ? AND door_height >= ?) OR (door_width >= ? AND door_height >= ?) ORDER BY popularity DESC, id ASC LIMIT ?"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.estateExists, "SELECT 1 FROM estate WHERE id = ? LIMIT 1"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.nazotteEstate, "SELECT "+estatePublicColumns+" FROM estate WHERE latitude <= ? AND latitude >= ? AND longitude <= ? AND longitude >= ? AND ST_Contains(ST_PolygonFromText(?), Point(latitude, longitude)) ORDER BY popularity DESC, id ASC LIMIT ?"); err != nil {
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
	nextEstate.SetMaxOpenConns(10)
	oldEstate := estateDB
	estateDB = nextEstate
	if oldEstate != nil {
		_ = oldEstate.Close()
	}

	nextChair, err := chairMySQLConnectionData.ConnectDB()
	if err != nil {
		return err
	}
	nextChair.SetMaxOpenConns(10)
	oldChair := chairDB
	chairDB = nextChair
	if oldChair != nil {
		_ = oldChair.Close()
	}
	return nil
}
