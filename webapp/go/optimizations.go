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
	mu         sync.RWMutex
	chairs     map[string]ChairSearchResponse
	estates    map[string]EstateSearchResponse
	generation uint64
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

func (cache *searchResponseCache) currentGeneration() uint64 {
	cache.mu.RLock()
	generation := cache.generation
	cache.mu.RUnlock()
	return generation
}

func (cache *searchResponseCache) putChair(key string, response ChairSearchResponse, generation uint64) {
	cache.mu.Lock()
	if generation != cache.generation {
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
	if generation != cache.generation {
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
	searchCache.generation++
	searchCache.mu.Unlock()
}

func invalidateEstateSearchCache() {
	searchCache.mu.Lock()
	searchCache.estates = make(map[string]EstateSearchResponse)
	searchCache.generation++
	searchCache.mu.Unlock()
}

func invalidateAllSearchCaches() {
	searchCache.mu.Lock()
	searchCache.chairs = make(map[string]ChairSearchResponse)
	searchCache.estates = make(map[string]EstateSearchResponse)
	searchCache.generation++
	searchCache.mu.Unlock()
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

func rebuildFeatureIndexes() error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := rebuildFeatureIndexTxForKind(tx, "chair"); err != nil {
		return err
	}
	if err := rebuildFeatureIndexTxForKind(tx, "estate"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	markFeatureIndexReady("chair")
	markFeatureIndexReady("estate")
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
	if unavailable || db == nil {
		return false
	}

	tx, err := db.Begin()
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

type preparedQuerySet struct {
	chairDetail       *sqlx.Stmt
	buyChair          *sqlx.Stmt
	lowPricedChair    *sqlx.Stmt
	estateDetail      *sqlx.Stmt
	lowPricedEstate   *sqlx.Stmt
	recommendedChair  *sqlx.Stmt
	recommendedEstate *sqlx.Stmt
	estateExists      *sqlx.Stmt
	nazotteEstate     *sqlx.Stmt
}

func (queries *preparedQuerySet) close() {
	if queries == nil {
		return
	}
	if queries.chairDetail != nil {
		_ = queries.chairDetail.Close()
	}
	if queries.buyChair != nil {
		_ = queries.buyChair.Close()
	}
	if queries.lowPricedChair != nil {
		_ = queries.lowPricedChair.Close()
	}
	if queries.estateDetail != nil {
		_ = queries.estateDetail.Close()
	}
	if queries.lowPricedEstate != nil {
		_ = queries.lowPricedEstate.Close()
	}
	if queries.recommendedChair != nil {
		_ = queries.recommendedChair.Close()
	}
	if queries.recommendedEstate != nil {
		_ = queries.recommendedEstate.Close()
	}
	if queries.estateExists != nil {
		_ = queries.estateExists.Close()
	}
	if queries.nazotteEstate != nil {
		_ = queries.nazotteEstate.Close()
	}
}

var preparedQueryState struct {
	sync.RWMutex
	queries     *preparedQuerySet
	unavailable bool
}

func prepareQuerySet() (*preparedQuerySet, error) {
	queries := &preparedQuerySet{}
	var err error
	prepare := func(destination **sqlx.Stmt, query string) error {
		*destination, err = db.Preparex(query)
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
	if err := prepare(&queries.estateDetail, "SELECT "+estatePublicColumns+" FROM estate WHERE id = ?"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.lowPricedEstate, "SELECT "+estatePublicColumns+" FROM estate ORDER BY rent ASC, id ASC LIMIT ?"); err != nil {
		queries.close()
		return nil, err
	}
	if err := prepare(&queries.recommendedChair, "SELECT width, height, depth FROM chair WHERE id = ?"); err != nil {
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

func preparedQueriesOrNil() *preparedQuerySet {
	preparedQueryState.RLock()
	queries := preparedQueryState.queries
	unavailable := preparedQueryState.unavailable
	preparedQueryState.RUnlock()
	if queries != nil || unavailable || db == nil {
		return queries
	}

	preparedQueryState.Lock()
	defer preparedQueryState.Unlock()
	if preparedQueryState.queries != nil || preparedQueryState.unavailable || db == nil {
		return preparedQueryState.queries
	}

	queries, err := prepareQuerySet()
	if err != nil {
		preparedQueryState.unavailable = true
		return nil
	}
	preparedQueryState.queries = queries
	return queries
}

func invalidatePreparedQueries() {
	preparedQueryState.Lock()
	queries := preparedQueryState.queries
	preparedQueryState.queries = nil
	preparedQueryState.unavailable = false
	preparedQueryState.Unlock()
	queries.close()
}

func reopenDBPool() error {
	next, err := mySQLConnectionData.ConnectDB()
	if err != nil {
		return err
	}
	next.SetMaxOpenConns(10)
	old := db
	db = next
	if old != nil {
		_ = old.Close()
	}
	return nil
}
