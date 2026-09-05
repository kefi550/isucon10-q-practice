package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/labstack/gommon/log"
)

const Limit = 20
const NazotteLimit = 50
const maxDBConnections = 10

// estate と chair は互いに独立なテーブルなので、別々の MySQL インスタンスに分割できる。
// estate は isu1 (アプリと同居)、chair は isu2 でホストする。
var estateDB *sqlx.DB
var chairDB *sqlx.DB
var estateMySQLConnectionData *MySQLConnectionEnv
var chairMySQLConnectionData *MySQLConnectionEnv
var chairSearchCondition ChairSearchCondition
var estateSearchCondition EstateSearchCondition

type InitializeResponse struct {
	Language string `json:"language"`
}

type Chair struct {
	ID          int64  `db:"id" json:"id"`
	Name        string `db:"name" json:"name"`
	Description string `db:"description" json:"description"`
	Thumbnail   string `db:"thumbnail" json:"thumbnail"`
	Price       int64  `db:"price" json:"price"`
	Height      int64  `db:"height" json:"height"`
	Width       int64  `db:"width" json:"width"`
	Depth       int64  `db:"depth" json:"depth"`
	Color       string `db:"color" json:"color"`
	Features    string `db:"features" json:"features"`
	Kind        string `db:"kind" json:"kind"`
	Popularity  int64  `db:"popularity" json:"-"`
	Stock       int64  `db:"stock" json:"-"`
}

type ChairSearchResponse struct {
	Count  int64   `json:"count"`
	Chairs []Chair `json:"chairs"`
}

type ChairListResponse struct {
	Chairs []Chair `json:"chairs"`
}

// Estate 物件
type Estate struct {
	ID          int64   `db:"id" json:"id"`
	Thumbnail   string  `db:"thumbnail" json:"thumbnail"`
	Name        string  `db:"name" json:"name"`
	Description string  `db:"description" json:"description"`
	Latitude    float64 `db:"latitude" json:"latitude"`
	Longitude   float64 `db:"longitude" json:"longitude"`
	Address     string  `db:"address" json:"address"`
	Rent        int64   `db:"rent" json:"rent"`
	DoorHeight  int64   `db:"door_height" json:"doorHeight"`
	DoorWidth   int64   `db:"door_width" json:"doorWidth"`
	Features    string  `db:"features" json:"features"`
	Popularity  int64   `db:"popularity" json:"-"`
}

// EstateSearchResponse estate/searchへのレスポンスの形式
type EstateSearchResponse struct {
	Count   int64    `json:"count"`
	Estates []Estate `json:"estates"`
}

type EstateListResponse struct {
	Estates []Estate `json:"estates"`
}

type Coordinate struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type Coordinates struct {
	Coordinates []Coordinate `json:"coordinates"`
}

type Range struct {
	ID  int64 `json:"id"`
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

type RangeCondition struct {
	Prefix string   `json:"prefix"`
	Suffix string   `json:"suffix"`
	Ranges []*Range `json:"ranges"`
}

type ListCondition struct {
	List []string `json:"list"`
}

type EstateSearchCondition struct {
	DoorWidth  RangeCondition `json:"doorWidth"`
	DoorHeight RangeCondition `json:"doorHeight"`
	Rent       RangeCondition `json:"rent"`
	Feature    ListCondition  `json:"feature"`
}

type ChairSearchCondition struct {
	Width   RangeCondition `json:"width"`
	Height  RangeCondition `json:"height"`
	Depth   RangeCondition `json:"depth"`
	Price   RangeCondition `json:"price"`
	Color   ListCondition  `json:"color"`
	Feature ListCondition  `json:"feature"`
	Kind    ListCondition  `json:"kind"`
}

type BoundingBox struct {
	// TopLeftCorner 緯度経度が共に最小値になるような点の情報を持っている
	TopLeftCorner Coordinate
	// BottomRightCorner 緯度経度が共に最大値になるような点の情報を持っている
	BottomRightCorner Coordinate
}

type MySQLConnectionEnv struct {
	Host     string
	Port     string
	User     string
	DBName   string
	Password string
}

type RecordMapper struct {
	Record []string

	offset int
	err    error
}

func (r *RecordMapper) next() (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if r.offset >= len(r.Record) {
		r.err = fmt.Errorf("too many read")
		return "", r.err
	}
	s := r.Record[r.offset]
	r.offset++
	return s, nil
}

func (r *RecordMapper) NextInt() int {
	s, err := r.next()
	if err != nil {
		return 0
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		r.err = err
		return 0
	}
	return i
}

func (r *RecordMapper) NextFloat() float64 {
	s, err := r.next()
	if err != nil {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		r.err = err
		return 0
	}
	return f
}

func (r *RecordMapper) NextString() string {
	s, err := r.next()
	if err != nil {
		return ""
	}
	return s
}

func (r *RecordMapper) Err() error {
	return r.err
}

// NewMySQLConnectionEnv は prefix+"MYSQL_HOST" 等の環境変数から接続情報を読む。
// estate 用は prefix="" (既存の MYSQL_* をそのまま使う、isu1 のローカル接続)、
// chair 用は prefix="CHAIR_" (CHAIR_MYSQL_* で isu2 を指す) を渡す。
func NewMySQLConnectionEnv(prefix string) *MySQLConnectionEnv {
	return &MySQLConnectionEnv{
		Host:     getEnv(prefix+"MYSQL_HOST", "127.0.0.1"),
		Port:     getEnv(prefix+"MYSQL_PORT", "3306"),
		User:     getEnv(prefix+"MYSQL_USER", "isucon"),
		DBName:   getEnv(prefix+"MYSQL_DBNAME", "isuumo"),
		Password: getEnv(prefix+"MYSQL_PASS", "isucon"),
	}
}

func getEnv(key, defaultValue string) string {
	val := os.Getenv(key)
	if val != "" {
		return val
	}
	return defaultValue
}

// ConnectDB isuumoデータベースに接続する
func (mc *MySQLConnectionEnv) ConnectDB() (*sqlx.DB, error) {
	// Dynamic search SQL cannot be prepared once because its predicates vary.
	// Driver-side interpolation keeps values escaped and parameterized while
	// avoiding a prepare/execute/close round trip, especially to remote chairDB.
	dsn := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v?interpolateParams=true", mc.User, mc.Password, mc.Host, mc.Port, mc.DBName)
	return sqlx.Open("mysql", dsn)
}

func configureDBPool(db *sqlx.DB) {
	db.SetMaxOpenConns(maxDBConnections)
	db.SetMaxIdleConns(maxDBConnections)
}

func init() {
	jsonText, err := ioutil.ReadFile("../fixture/chair_condition.json")
	if err != nil {
		fmt.Printf("%v\n", err)
		os.Exit(1)
	}
	json.Unmarshal(jsonText, &chairSearchCondition)

	jsonText, err = ioutil.ReadFile("../fixture/estate_condition.json")
	if err != nil {
		fmt.Printf("%v\n", err)
		os.Exit(1)
	}
	json.Unmarshal(jsonText, &estateSearchCondition)
}

func main() {
	// Echo instance
	e := echo.New()
	e.Debug = true
	e.Logger.SetLevel(log.DEBUG)

	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	// Initialize
	e.POST("/initialize", initialize)

	// Chair Handler
	e.GET("/api/chair/:id", getChairDetail)
	e.POST("/api/chair", postChair)
	e.GET("/api/chair/search", searchChairs)
	e.GET("/api/chair/low_priced", getLowPricedChair)
	e.GET("/api/chair/search/condition", getChairSearchCondition)
	e.POST("/api/chair/buy/:id", buyChair)

	// Estate Handler
	e.GET("/api/estate/:id", getEstateDetail)
	e.POST("/api/estate", postEstate)
	e.GET("/api/estate/search", searchEstates)
	e.GET("/api/estate/low_priced", getLowPricedEstate)
	e.POST("/api/estate/req_doc/:id", postEstateRequestDocument)
	e.POST("/api/estate/nazotte", searchEstateNazotte)
	e.GET("/api/estate/search/condition", getEstateSearchCondition)
	e.GET("/api/recommended_estate/:id", searchRecommendedEstateWithChair)

	estateMySQLConnectionData = NewMySQLConnectionEnv("")
	chairMySQLConnectionData = NewMySQLConnectionEnv("CHAIR_")

	var err error
	estateDB, err = estateMySQLConnectionData.ConnectDB()
	if err != nil {
		e.Logger.Fatalf("Estate DB connection failed : %v", err)
	}
	configureDBPool(estateDB)
	defer estateDB.Close()

	chairDB, err = chairMySQLConnectionData.ConnectDB()
	if err != nil {
		e.Logger.Fatalf("Chair DB connection failed : %v", err)
	}
	configureDBPool(chairDB)
	defer chairDB.Close()

	// Start server
	serverPort := fmt.Sprintf(":%v", getEnv("SERVER_PORT", "1323"))
	e.Logger.Fatal(e.Start(serverPort))
}

func initialize(c echo.Context) error {
	invalidateAllSearchCaches()
	resetReadCache()
	resetFeatureIndexState()
	invalidateChairPreparedQueries()
	invalidateEstatePreparedQueries()

	sqlDir := filepath.Join("..", "mysql", "db")

	estatePaths := []string{
		filepath.Join(sqlDir, "0_Schema_Estate.sql"),
		filepath.Join(sqlDir, "1_DummyEstateData.sql"),
	}
	chairPaths := []string{
		filepath.Join(sqlDir, "0_Schema_Chair.sql"),
		filepath.Join(sqlDir, "2_DummyChairData.sql"),
	}

	if err := runSQLFiles(estateMySQLConnectionData, estatePaths); err != nil {
		c.Logger().Errorf("Initialize script error (estate) : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	if err := runSQLFiles(chairMySQLConnectionData, chairPaths); err != nil {
		c.Logger().Errorf("Initialize script error (chair) : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	if err := reopenDBPool(); err != nil {
		c.Logger().Errorf("failed to reopen DB pool after initialize: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	if err := rebuildFeatureIndexes(); err != nil {
		c.Logger().Errorf("failed to rebuild feature indexes: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	// Do this again after the database swap so a request that overlapped
	// initialization cannot leave a result from the previous generation behind.
	invalidateAllSearchCaches()
	resetReadCache()

	return c.JSON(http.StatusOK, InitializeResponse{
		Language: "go",
	})
}

// runSQLFiles は指定した MySQL 接続先に対して、SQL ファイルを順番に mysql コマンドで流し込む。
// このプロセス (アプリサーバー) から見て conn がリモートホストを指していても、
// -h でホストを指定するだけでよい。
func runSQLFiles(conn *MySQLConnectionEnv, paths []string) error {
	for _, p := range paths {
		sqlFile, _ := filepath.Abs(p)
		cmdStr := fmt.Sprintf("mysql -h %v -u %v -p%v -P %v %v < %v",
			conn.Host,
			conn.User,
			conn.Password,
			conn.Port,
			conn.DBName,
			sqlFile,
		)
		if err := exec.Command("bash", "-c", cmdStr).Run(); err != nil {
			return err
		}
	}
	return nil
}

func getChairDetail(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.Echo().Logger.Errorf("Request parameter \"id\" parse error : %v", err)
		return c.NoContent(http.StatusBadRequest)
	}
	if chair, ok := getCachedChair(id); ok {
		return c.JSON(http.StatusOK, chair)
	}
	cacheGeneration := currentChairReadGeneration()

	chair := Chair{}
	if queries := chairPreparedQueriesOrNil(); queries != nil {
		err = queries.chairDetail.Get(&chair, id)
	} else {
		err = chairDB.Get(&chair, "SELECT "+chairDetailColumns+" FROM chair WHERE id = ?", id)
	}
	if err != nil {
		if err == sql.ErrNoRows {
			c.Echo().Logger.Infof("requested id's chair not found : %v", id)
			return c.NoContent(http.StatusNotFound)
		}
		c.Echo().Logger.Errorf("Failed to get the chair from id : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	} else if chair.Stock <= 0 {
		c.Echo().Logger.Infof("requested id's chair is sold out : %v", id)
		return c.NoContent(http.StatusNotFound)
	}
	rememberChair(chair, cacheGeneration)

	return c.JSON(http.StatusOK, chair)
}

const insertBatchSize = 500

func insertBatch(tx *sql.Tx, table string, columns []string, values [][]interface{}) error {
	if len(values) == 0 {
		return nil
	}

	placeholders := make([]string, len(columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	rowPlaceholder := "(" + strings.Join(placeholders, ",") + ")"
	valuePlaceholders := make([]string, len(values))
	args := make([]interface{}, 0, len(values)*len(columns))
	for i, value := range values {
		if len(value) != len(columns) {
			return fmt.Errorf("unexpected number of values for %s: got %d, want %d", table, len(value), len(columns))
		}
		valuePlaceholders[i] = rowPlaceholder
		args = append(args, value...)
	}

	query := fmt.Sprintf("INSERT INTO %s(%s) VALUES %s", table, strings.Join(columns, ", "), strings.Join(valuePlaceholders, ","))
	_, err := tx.Exec(query, args...)
	return err
}

func postChair(c echo.Context) error {
	header, err := c.FormFile("chairs")
	if err != nil {
		c.Logger().Errorf("failed to get form file: %v", err)
		return c.NoContent(http.StatusBadRequest)
	}
	f, err := header.Open()
	if err != nil {
		c.Logger().Errorf("failed to open form file: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer f.Close()
	reader := csv.NewReader(f)

	tx, err := chairDB.Begin()
	if err != nil {
		c.Logger().Errorf("failed to begin tx: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer tx.Rollback()
	chairColumns := []string{"id", "name", "description", "thumbnail", "price", "height", "width", "depth", "color", "features", "kind", "popularity", "stock"}
	chairBatch := make([][]interface{}, 0, insertBatchSize)
	chairFeatureIndexReady := isFeatureIndexReady("chair")
	chairFeatureValues := uniqueFeatureValues(chairSearchCondition.Feature.List)
	chairFeatureBatch := make([][]interface{}, 0, insertBatchSize)
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			c.Logger().Errorf("failed to read csv: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}

		rm := RecordMapper{Record: row}
		id := rm.NextInt()
		name := rm.NextString()
		description := rm.NextString()
		thumbnail := rm.NextString()
		price := rm.NextInt()
		height := rm.NextInt()
		width := rm.NextInt()
		depth := rm.NextInt()
		color := rm.NextString()
		features := rm.NextString()
		kind := rm.NextString()
		popularity := rm.NextInt()
		stock := rm.NextInt()
		if err := rm.Err(); err != nil {
			c.Logger().Errorf("failed to read record: %v", err)
			return c.NoContent(http.StatusBadRequest)
		}
		chairBatch = append(chairBatch, []interface{}{id, name, description, thumbnail, price, height, width, depth, color, features, kind, popularity, stock})
		if chairFeatureIndexReady {
			chairFeatureBatch = appendFeatureIndexRows(chairFeatureBatch, id, features, chairFeatureValues)
		}
		if len(chairBatch) < insertBatchSize {
			continue
		}
		if err := insertBatch(tx, "chair", chairColumns, chairBatch); err != nil {
			c.Logger().Errorf("failed to insert chair: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
		if chairFeatureIndexReady {
			if err := insertFeatureIndexBatch(tx, "chair", chairFeatureBatch); err != nil {
				c.Logger().Errorf("failed to insert chair feature index: %v", err)
				return c.NoContent(http.StatusInternalServerError)
			}
		}
		chairBatch = chairBatch[:0]
		chairFeatureBatch = chairFeatureBatch[:0]
	}
	if err := insertBatch(tx, "chair", chairColumns, chairBatch); err != nil {
		c.Logger().Errorf("failed to insert chair: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	if chairFeatureIndexReady {
		if err := insertFeatureIndexBatch(tx, "chair", chairFeatureBatch); err != nil {
			c.Logger().Errorf("failed to insert chair feature index: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
	}
	if err := tx.Commit(); err != nil {
		c.Logger().Errorf("failed to commit tx: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	invalidateChairSearchCache()
	invalidateChairReadCaches()
	return c.NoContent(http.StatusCreated)
}

func searchChairs(c echo.Context) error {
	conditions := make([]string, 0)
	params := make([]interface{}, 0)

	if c.QueryParam("priceRangeId") != "" {
		chairPrice, err := getRange(chairSearchCondition.Price, c.QueryParam("priceRangeId"))
		if err != nil {
			c.Echo().Logger.Infof("priceRangeID invalid, %v : %v", c.QueryParam("priceRangeId"), err)
			return c.NoContent(http.StatusBadRequest)
		}

		if chairPrice.Min != -1 {
			conditions = append(conditions, "price >= ?")
			params = append(params, chairPrice.Min)
		}
		if chairPrice.Max != -1 {
			conditions = append(conditions, "price < ?")
			params = append(params, chairPrice.Max)
		}
	}

	if c.QueryParam("heightRangeId") != "" {
		chairHeight, err := getRange(chairSearchCondition.Height, c.QueryParam("heightRangeId"))
		if err != nil {
			c.Echo().Logger.Infof("heightRangeIf invalid, %v : %v", c.QueryParam("heightRangeId"), err)
			return c.NoContent(http.StatusBadRequest)
		}

		if chairHeight.Min != -1 {
			conditions = append(conditions, "height >= ?")
			params = append(params, chairHeight.Min)
		}
		if chairHeight.Max != -1 {
			conditions = append(conditions, "height < ?")
			params = append(params, chairHeight.Max)
		}
	}

	if c.QueryParam("widthRangeId") != "" {
		chairWidth, err := getRange(chairSearchCondition.Width, c.QueryParam("widthRangeId"))
		if err != nil {
			c.Echo().Logger.Infof("widthRangeID invalid, %v : %v", c.QueryParam("widthRangeId"), err)
			return c.NoContent(http.StatusBadRequest)
		}

		if chairWidth.Min != -1 {
			conditions = append(conditions, "width >= ?")
			params = append(params, chairWidth.Min)
		}
		if chairWidth.Max != -1 {
			conditions = append(conditions, "width < ?")
			params = append(params, chairWidth.Max)
		}
	}

	if c.QueryParam("depthRangeId") != "" {
		chairDepth, err := getRange(chairSearchCondition.Depth, c.QueryParam("depthRangeId"))
		if err != nil {
			c.Echo().Logger.Infof("depthRangeId invalid, %v : %v", c.QueryParam("depthRangeId"), err)
			return c.NoContent(http.StatusBadRequest)
		}

		if chairDepth.Min != -1 {
			conditions = append(conditions, "depth >= ?")
			params = append(params, chairDepth.Min)
		}
		if chairDepth.Max != -1 {
			conditions = append(conditions, "depth < ?")
			params = append(params, chairDepth.Max)
		}
	}

	if c.QueryParam("kind") != "" {
		conditions = append(conditions, "kind = ?")
		params = append(params, c.QueryParam("kind"))
	}

	if c.QueryParam("color") != "" {
		conditions = append(conditions, "color = ?")
		params = append(params, c.QueryParam("color"))
	}

	featureQuery := c.QueryParam("features")
	useChairFeatureIndex := featureQuery != "" &&
		hasConfiguredFeatureQuery(featureQuery, chairSearchCondition.Feature.List) &&
		ensureChairFeatureIndex()
	if featureQuery != "" {
		for _, f := range strings.Split(featureQuery, ",") {
			if useChairFeatureIndex && isConfiguredFeature(f, chairSearchCondition.Feature.List) {
				conditions = append(conditions, "EXISTS (SELECT 1 FROM chair_feature AS cf WHERE cf.chair_id = chair.id AND cf.feature_value = ?)")
			} else {
				conditions = append(conditions, "features LIKE CONCAT('%', ?, '%')")
			}
			params = append(params, f)
		}
	}

	if len(conditions) == 0 {
		c.Echo().Logger.Infof("Search condition not found")
		return c.NoContent(http.StatusBadRequest)
	}

	conditions = append(conditions, "stock > 0")

	page, err := strconv.Atoi(c.QueryParam("page"))
	if err != nil {
		c.Logger().Infof("Invalid format page parameter : %v", err)
		return c.NoContent(http.StatusBadRequest)
	}

	perPage, err := strconv.Atoi(c.QueryParam("perPage"))
	if err != nil {
		c.Logger().Infof("Invalid format perPage parameter : %v", err)
		return c.NoContent(http.StatusBadRequest)
	}

	searchQuery := "SELECT " + chairPublicColumns + " FROM chair AS chair WHERE "
	countQuery := "SELECT COUNT(*) FROM chair AS chair WHERE "
	searchCondition := strings.Join(conditions, " AND ")
	limitOffset := " ORDER BY popularity_desc ASC, id ASC LIMIT ? OFFSET ?"
	cacheKey := c.Request().URL.Query().Encode()
	countValues := c.Request().URL.Query()
	countValues.Del("page")
	countValues.Del("perPage")
	countCacheKey := countValues.Encode()
	cacheGeneration := searchCache.currentChairGeneration()
	if cached, ok := searchCache.getChair(cacheKey); ok {
		return c.JSON(http.StatusOK, cached)
	}

	res, err := loadChairSearchOnce(cacheKey, cacheGeneration, func() (ChairSearchResponse, error) {
		if cached, ok := searchCache.getChair(cacheKey); ok {
			return cached, nil
		}
		count, countErr := loadChairCountOnce(countCacheKey, cacheGeneration, func() (int64, error) {
			if cachedCount, ok := searchCache.getChairCount(countCacheKey); ok {
				return cachedCount, nil
			}
			var loadedCount int64
			if loadErr := chairDB.Get(&loadedCount, countQuery+searchCondition, params...); loadErr != nil {
				return 0, loadErr
			}
			searchCache.putChairCount(countCacheKey, loadedCount, cacheGeneration)
			return loadedCount, nil
		})
		if countErr != nil {
			return ChairSearchResponse{}, countErr
		}

		chairs := []Chair{}
		searchParams := append(append([]interface{}{}, params...), perPage, page*perPage)
		if loadErr := chairDB.Select(&chairs, searchQuery+searchCondition+limitOffset, searchParams...); loadErr != nil {
			return ChairSearchResponse{}, loadErr
		}
		loaded := ChairSearchResponse{Count: count, Chairs: chairs}
		searchCache.putChair(cacheKey, loaded, cacheGeneration)
		return loaded, nil
	})
	if err != nil {
		c.Logger().Errorf("searchChairs DB execution error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, res)
}

func buyChair(c echo.Context) error {
	m := echo.Map{}
	if err := c.Bind(&m); err != nil {
		c.Echo().Logger.Infof("post buy chair failed : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	_, ok := m["email"].(string)
	if !ok {
		c.Echo().Logger.Info("post buy chair failed : email not found in request body")
		return c.NoContent(http.StatusBadRequest)
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.Echo().Logger.Infof("post buy chair failed : %v", err)
		return c.NoContent(http.StatusBadRequest)
	}

	var result sql.Result
	if queries := chairPreparedQueriesOrNil(); queries != nil {
		result, err = queries.buyChair.Exec(id)
	} else {
		result, err = chairDB.Exec("UPDATE chair SET stock = LAST_INSERT_ID(stock - 1) WHERE id = ? AND stock > 0", id)
	}
	if err != nil {
		c.Echo().Logger.Errorf("chair stock update failed: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	updated, err := result.RowsAffected()
	if err != nil {
		c.Echo().Logger.Errorf("failed to inspect chair stock update: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	if updated == 0 {
		c.Echo().Logger.Infof("buyChair chair id \"%v\" not found", id)
		return c.NoContent(http.StatusNotFound)
	}

	remainingStock, stockErr := result.LastInsertId()
	if stockErr != nil || remainingStock == 0 {
		invalidateChairSearchCache()
		forgetChair(id)
	}

	return c.NoContent(http.StatusOK)
}

func getChairSearchCondition(c echo.Context) error {
	return c.JSON(http.StatusOK, chairSearchCondition)
}

func getLowPricedChair(c echo.Context) error {
	if chairs, ok := getCachedLowPricedChairs(); ok {
		return c.JSON(http.StatusOK, ChairListResponse{Chairs: chairs})
	}
	cacheGeneration := currentChairReadGeneration()
	var chairs []Chair
	var err error
	if queries := chairPreparedQueriesOrNil(); queries != nil {
		err = queries.lowPricedChair.Select(&chairs, Limit)
	} else {
		err = chairDB.Select(&chairs, "SELECT "+chairPublicColumns+" FROM chair WHERE stock > 0 ORDER BY price ASC, id ASC LIMIT ?", Limit)
	}
	if err != nil {
		if err == sql.ErrNoRows {
			c.Logger().Error("getLowPricedChair not found")
			return c.JSON(http.StatusOK, ChairListResponse{[]Chair{}})
		}
		c.Logger().Errorf("getLowPricedChair DB execution error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	cacheLowPricedChairs(chairs, cacheGeneration)

	return c.JSON(http.StatusOK, ChairListResponse{Chairs: chairs})
}

func getEstateDetail(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.Echo().Logger.Infof("Request parameter \"id\" parse error : %v", err)
		return c.NoContent(http.StatusBadRequest)
	}
	if estate, ok := getCachedEstate(id); ok {
		return c.JSON(http.StatusOK, estate)
	}

	var estate Estate
	if queries := estatePreparedQueriesOrNil(); queries != nil {
		err = queries.estateDetail.Get(&estate, id)
	} else {
		err = estateDB.Get(&estate, "SELECT "+estatePublicColumns+" FROM estate WHERE id = ?", id)
	}
	if err != nil {
		if err == sql.ErrNoRows {
			c.Echo().Logger.Infof("getEstateDetail estate id %v not found", id)
			return c.NoContent(http.StatusNotFound)
		}
		c.Echo().Logger.Errorf("Database Execution error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	rememberEstate(estate)

	return c.JSON(http.StatusOK, estate)
}

func getRange(cond RangeCondition, rangeID string) (*Range, error) {
	RangeIndex, err := strconv.Atoi(rangeID)
	if err != nil {
		return nil, err
	}

	if RangeIndex < 0 || len(cond.Ranges) <= RangeIndex {
		return nil, fmt.Errorf("Unexpected Range ID")
	}

	return cond.Ranges[RangeIndex], nil
}

func postEstate(c echo.Context) error {
	header, err := c.FormFile("estates")
	if err != nil {
		c.Logger().Errorf("failed to get form file: %v", err)
		return c.NoContent(http.StatusBadRequest)
	}
	f, err := header.Open()
	if err != nil {
		c.Logger().Errorf("failed to open form file: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer f.Close()
	reader := csv.NewReader(f)

	tx, err := estateDB.Begin()
	if err != nil {
		c.Logger().Errorf("failed to begin tx: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer tx.Rollback()
	estateColumns := []string{"id", "name", "description", "thumbnail", "address", "latitude", "longitude", "rent", "door_height", "door_width", "features", "popularity"}
	estateBatch := make([][]interface{}, 0, insertBatchSize)
	estateFeatureIndexReady := isFeatureIndexReady("estate")
	estateFeatureValues := uniqueFeatureValues(estateSearchCondition.Feature.List)
	estateFeatureBatch := make([][]interface{}, 0, insertBatchSize)
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			c.Logger().Errorf("failed to read csv: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}

		rm := RecordMapper{Record: row}
		id := rm.NextInt()
		name := rm.NextString()
		description := rm.NextString()
		thumbnail := rm.NextString()
		address := rm.NextString()
		latitude := rm.NextFloat()
		longitude := rm.NextFloat()
		rent := rm.NextInt()
		doorHeight := rm.NextInt()
		doorWidth := rm.NextInt()
		features := rm.NextString()
		popularity := rm.NextInt()
		if err := rm.Err(); err != nil {
			c.Logger().Errorf("failed to read record: %v", err)
			return c.NoContent(http.StatusBadRequest)
		}
		estateBatch = append(estateBatch, []interface{}{id, name, description, thumbnail, address, latitude, longitude, rent, doorHeight, doorWidth, features, popularity})
		if estateFeatureIndexReady {
			estateFeatureBatch = appendFeatureIndexRows(estateFeatureBatch, id, features, estateFeatureValues)
		}
		if len(estateBatch) < insertBatchSize {
			continue
		}
		if err := insertBatch(tx, "estate", estateColumns, estateBatch); err != nil {
			c.Logger().Errorf("failed to insert estate: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
		if estateFeatureIndexReady {
			if err := insertFeatureIndexBatch(tx, "estate", estateFeatureBatch); err != nil {
				c.Logger().Errorf("failed to insert estate feature index: %v", err)
				return c.NoContent(http.StatusInternalServerError)
			}
		}
		estateBatch = estateBatch[:0]
		estateFeatureBatch = estateFeatureBatch[:0]
	}
	if err := insertBatch(tx, "estate", estateColumns, estateBatch); err != nil {
		c.Logger().Errorf("failed to insert estate: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	if estateFeatureIndexReady {
		if err := insertFeatureIndexBatch(tx, "estate", estateFeatureBatch); err != nil {
			c.Logger().Errorf("failed to insert estate feature index: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
	}
	if err := tx.Commit(); err != nil {
		c.Logger().Errorf("failed to commit tx: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	invalidateEstateSearchCache()
	invalidateEstateReadCaches()
	return c.NoContent(http.StatusCreated)
}

func searchEstates(c echo.Context) error {
	conditions := make([]string, 0)
	params := make([]interface{}, 0)

	if c.QueryParam("doorHeightRangeId") != "" {
		doorHeight, err := getRange(estateSearchCondition.DoorHeight, c.QueryParam("doorHeightRangeId"))
		if err != nil {
			c.Echo().Logger.Infof("doorHeightRangeID invalid, %v : %v", c.QueryParam("doorHeightRangeId"), err)
			return c.NoContent(http.StatusBadRequest)
		}

		if doorHeight.Min != -1 {
			conditions = append(conditions, "door_height >= ?")
			params = append(params, doorHeight.Min)
		}
		if doorHeight.Max != -1 {
			conditions = append(conditions, "door_height < ?")
			params = append(params, doorHeight.Max)
		}
	}

	if c.QueryParam("doorWidthRangeId") != "" {
		doorWidth, err := getRange(estateSearchCondition.DoorWidth, c.QueryParam("doorWidthRangeId"))
		if err != nil {
			c.Echo().Logger.Infof("doorWidthRangeID invalid, %v : %v", c.QueryParam("doorWidthRangeId"), err)
			return c.NoContent(http.StatusBadRequest)
		}

		if doorWidth.Min != -1 {
			conditions = append(conditions, "door_width >= ?")
			params = append(params, doorWidth.Min)
		}
		if doorWidth.Max != -1 {
			conditions = append(conditions, "door_width < ?")
			params = append(params, doorWidth.Max)
		}
	}

	if c.QueryParam("rentRangeId") != "" {
		estateRent, err := getRange(estateSearchCondition.Rent, c.QueryParam("rentRangeId"))
		if err != nil {
			c.Echo().Logger.Infof("rentRangeID invalid, %v : %v", c.QueryParam("rentRangeId"), err)
			return c.NoContent(http.StatusBadRequest)
		}

		if estateRent.Min != -1 {
			conditions = append(conditions, "rent >= ?")
			params = append(params, estateRent.Min)
		}
		if estateRent.Max != -1 {
			conditions = append(conditions, "rent < ?")
			params = append(params, estateRent.Max)
		}
	}

	featureQuery := c.QueryParam("features")
	useEstateFeatureIndex := featureQuery != "" &&
		hasConfiguredFeatureQuery(featureQuery, estateSearchCondition.Feature.List) &&
		ensureEstateFeatureIndex()
	if featureQuery != "" {
		for _, f := range strings.Split(featureQuery, ",") {
			if useEstateFeatureIndex && isConfiguredFeature(f, estateSearchCondition.Feature.List) {
				conditions = append(conditions, "EXISTS (SELECT 1 FROM estate_feature AS ef WHERE ef.estate_id = estate.id AND ef.feature_value = ?)")
			} else {
				conditions = append(conditions, "features like concat('%', ?, '%')")
			}
			params = append(params, f)
		}
	}

	if len(conditions) == 0 {
		c.Echo().Logger.Infof("searchEstates search condition not found")
		return c.NoContent(http.StatusBadRequest)
	}

	page, err := strconv.Atoi(c.QueryParam("page"))
	if err != nil {
		c.Logger().Infof("Invalid format page parameter : %v", err)
		return c.NoContent(http.StatusBadRequest)
	}

	perPage, err := strconv.Atoi(c.QueryParam("perPage"))
	if err != nil {
		c.Logger().Infof("Invalid format perPage parameter : %v", err)
		return c.NoContent(http.StatusBadRequest)
	}

	searchQuery := "SELECT " + estatePublicColumns + " FROM estate AS estate WHERE "
	countQuery := "SELECT COUNT(*) FROM estate AS estate WHERE "
	searchCondition := strings.Join(conditions, " AND ")
	limitOffset := " ORDER BY popularity_desc ASC, id ASC LIMIT ? OFFSET ?"
	cacheKey := c.Request().URL.Query().Encode()
	countValues := c.Request().URL.Query()
	countValues.Del("page")
	countValues.Del("perPage")
	countCacheKey := countValues.Encode()
	cacheGeneration := searchCache.currentEstateGeneration()
	if cached, ok := searchCache.getEstate(cacheKey); ok {
		return c.JSON(http.StatusOK, cached)
	}

	res, err := loadEstateSearchOnce(cacheKey, cacheGeneration, func() (EstateSearchResponse, error) {
		if cached, ok := searchCache.getEstate(cacheKey); ok {
			return cached, nil
		}
		count, countErr := loadEstateCountOnce(countCacheKey, cacheGeneration, func() (int64, error) {
			if cachedCount, ok := searchCache.getEstateCount(countCacheKey); ok {
				return cachedCount, nil
			}
			var loadedCount int64
			if loadErr := estateDB.Get(&loadedCount, countQuery+searchCondition, params...); loadErr != nil {
				return 0, loadErr
			}
			searchCache.putEstateCount(countCacheKey, loadedCount, cacheGeneration)
			return loadedCount, nil
		})
		if countErr != nil {
			return EstateSearchResponse{}, countErr
		}

		estates := []Estate{}
		searchParams := append(append([]interface{}{}, params...), perPage, page*perPage)
		if loadErr := estateDB.Select(&estates, searchQuery+searchCondition+limitOffset, searchParams...); loadErr != nil {
			return EstateSearchResponse{}, loadErr
		}
		loaded := EstateSearchResponse{Count: count, Estates: estates}
		rememberEstates(estates)
		searchCache.putEstate(cacheKey, loaded, cacheGeneration)
		return loaded, nil
	})
	if err != nil {
		c.Logger().Errorf("searchEstates DB execution error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, res)
}

func getLowPricedEstate(c echo.Context) error {
	if estates, ok := getCachedLowPricedEstates(); ok {
		return c.JSON(http.StatusOK, EstateListResponse{Estates: estates})
	}
	cacheGeneration := currentEstateReadGeneration()
	estates := make([]Estate, 0, Limit)
	var err error
	if queries := estatePreparedQueriesOrNil(); queries != nil {
		err = queries.lowPricedEstate.Select(&estates, Limit)
	} else {
		err = estateDB.Select(&estates, "SELECT "+estatePublicColumns+" FROM estate ORDER BY rent ASC, id ASC LIMIT ?", Limit)
	}
	if err != nil {
		if err == sql.ErrNoRows {
			c.Logger().Error("getLowPricedEstate not found")
			return c.JSON(http.StatusOK, EstateListResponse{[]Estate{}})
		}
		c.Logger().Errorf("getLowPricedEstate DB execution error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	rememberEstates(estates)
	cacheLowPricedEstates(estates, cacheGeneration)

	return c.JSON(http.StatusOK, EstateListResponse{Estates: estates})
}

func searchRecommendedEstateWithChair(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.Logger().Infof("Invalid format searchRecommendedEstateWithChair id : %v", err)
		return c.NoContent(http.StatusBadRequest)
	}

	// chair と estate は別インスタンスの MySQL に分かれているため JOIN はできない。
	// chair を引いてから estate を引く2クエリ構成のまま、それぞれ対応する DB に振り分ける。
	var dimensions [3]int64
	if cachedDimensions, ok := getCachedChairDimensions(id); ok {
		dimensions = cachedDimensions
	} else {
		chair := Chair{}
		if queries := chairPreparedQueriesOrNil(); queries != nil {
			err = queries.recommendedChair.Get(&chair, id)
		} else {
			err = chairDB.Get(&chair, "SELECT width, height, depth FROM chair WHERE id = ?", id)
		}
		if err != nil {
			if err == sql.ErrNoRows {
				c.Logger().Infof("Requested chair id \"%v\" not found", id)
				return c.NoContent(http.StatusBadRequest)
			}
			c.Logger().Errorf("Database execution error : %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
		dimensions = [3]int64{chair.Width, chair.Height, chair.Depth}
		rememberChairDimensions(id, chair.Width, chair.Height, chair.Depth)
	}

	var estates []Estate
	// 椅子がドアを通れるかどうかは、3辺のうち "最小の2辺" が door_width/door_height に
	// (順不同で) 収まるかだけで判定できる。大きい辺を含む組み合わせは常によりきつい条件に
	// なるため、6通りの順列を試す必要はなく、最小2辺による2パターンに帰着する。
	sides := []int64{dimensions[0], dimensions[1], dimensions[2]}
	sort.Slice(sides, func(i, j int) bool { return sides[i] < sides[j] })
	lo, mid := sides[0], sides[1]
	if estates, ok := getCachedRecommendedEstates(lo, mid); ok {
		return c.JSON(http.StatusOK, EstateListResponse{Estates: estates})
	}
	cacheGeneration := currentEstateReadGeneration()
	estates, err = loadRecommendedEstatesOnce(lo, mid, cacheGeneration, func() ([]Estate, error) {
		if cached, ok := getCachedRecommendedEstates(lo, mid); ok {
			return cached, nil
		}
		loaded := []Estate{}
		var loadErr error
		if queries := estatePreparedQueriesOrNil(); queries != nil {
			loadErr = queries.recommendedEstate.Select(&loaded, lo, mid, mid, lo, Limit)
		} else {
			loadErr = estateDB.Select(&loaded, "SELECT "+estatePublicColumns+" FROM estate WHERE (door_width >= ? AND door_height >= ?) OR (door_width >= ? AND door_height >= ?) ORDER BY popularity_desc ASC, id ASC LIMIT ?", lo, mid, mid, lo, Limit)
		}
		if loadErr != nil {
			return nil, loadErr
		}
		rememberEstates(loaded)
		cacheRecommendedEstates(lo, mid, loaded, cacheGeneration)
		return loaded, nil
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusOK, EstateListResponse{[]Estate{}})
		}
		c.Logger().Errorf("Database execution error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, EstateListResponse{Estates: estates})
}

func searchEstateNazotte(c echo.Context) error {
	coordinates := Coordinates{}
	err := c.Bind(&coordinates)
	if err != nil {
		c.Echo().Logger.Infof("post search estate nazotte failed : %v", err)
		return c.NoContent(http.StatusBadRequest)
	}

	if len(coordinates.Coordinates) == 0 {
		return c.NoContent(http.StatusBadRequest)
	}

	b := coordinates.getBoundingBox()
	estates := []Estate{}
	polygonText := coordinates.coordinatesToText()
	if queries := estatePreparedQueriesOrNil(); queries != nil {
		err = queries.nazotteEstate.Select(&estates, b.BottomRightCorner.Latitude, b.TopLeftCorner.Latitude, b.BottomRightCorner.Longitude, b.TopLeftCorner.Longitude, polygonText, NazotteLimit)
	} else {
		query := "SELECT " + estatePublicColumns + " FROM estate FORCE INDEX (idx_estate_latitude_longitude) WHERE latitude <= ? AND latitude >= ? AND longitude <= ? AND longitude >= ? AND ST_Contains(ST_PolygonFromText(?), Point(latitude, longitude)) ORDER BY popularity_desc ASC, id ASC LIMIT ?"
		err = estateDB.Select(&estates, query, b.BottomRightCorner.Latitude, b.TopLeftCorner.Latitude, b.BottomRightCorner.Longitude, b.TopLeftCorner.Longitude, polygonText, NazotteLimit)
	}
	if err == sql.ErrNoRows {
		c.Echo().Logger.Infof("select * from estate where latitude ...", err)
		return c.JSON(http.StatusOK, EstateSearchResponse{Count: 0, Estates: []Estate{}})
	} else if err != nil {
		c.Echo().Logger.Errorf("database execution error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	rememberEstates(estates)

	return c.JSON(http.StatusOK, EstateSearchResponse{Count: int64(len(estates)), Estates: estates})
}

func postEstateRequestDocument(c echo.Context) error {
	m := echo.Map{}
	if err := c.Bind(&m); err != nil {
		c.Echo().Logger.Infof("post request document failed : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	_, ok := m["email"].(string)
	if !ok {
		c.Echo().Logger.Info("post request document failed : email not found in request body")
		return c.NoContent(http.StatusBadRequest)
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.Echo().Logger.Infof("post request document failed : %v", err)
		return c.NoContent(http.StatusBadRequest)
	}
	if isKnownEstate(id) {
		return c.NoContent(http.StatusOK)
	}

	var exists int
	if queries := estatePreparedQueriesOrNil(); queries != nil {
		err = queries.estateExists.Get(&exists, id)
	} else {
		err = estateDB.Get(&exists, "SELECT 1 FROM estate WHERE id = ? LIMIT 1", id)
	}
	if err != nil {
		if err == sql.ErrNoRows {
			return c.NoContent(http.StatusNotFound)
		}
		c.Logger().Errorf("postEstateRequestDocument DB execution error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	rememberEstateID(id)

	return c.NoContent(http.StatusOK)
}

func getEstateSearchCondition(c echo.Context) error {
	return c.JSON(http.StatusOK, estateSearchCondition)
}

func (cs Coordinates) getBoundingBox() BoundingBox {
	coordinates := cs.Coordinates
	boundingBox := BoundingBox{
		TopLeftCorner: Coordinate{
			Latitude: coordinates[0].Latitude, Longitude: coordinates[0].Longitude,
		},
		BottomRightCorner: Coordinate{
			Latitude: coordinates[0].Latitude, Longitude: coordinates[0].Longitude,
		},
	}
	for _, coordinate := range coordinates {
		if boundingBox.TopLeftCorner.Latitude > coordinate.Latitude {
			boundingBox.TopLeftCorner.Latitude = coordinate.Latitude
		}
		if boundingBox.TopLeftCorner.Longitude > coordinate.Longitude {
			boundingBox.TopLeftCorner.Longitude = coordinate.Longitude
		}

		if boundingBox.BottomRightCorner.Latitude < coordinate.Latitude {
			boundingBox.BottomRightCorner.Latitude = coordinate.Latitude
		}
		if boundingBox.BottomRightCorner.Longitude < coordinate.Longitude {
			boundingBox.BottomRightCorner.Longitude = coordinate.Longitude
		}
	}
	return boundingBox
}

func (cs Coordinates) coordinatesToText() string {
	points := make([]string, 0, len(cs.Coordinates))
	for _, c := range cs.Coordinates {
		points = append(points, fmt.Sprintf("%f %f", c.Latitude, c.Longitude))
	}
	return fmt.Sprintf("POLYGON((%s))", strings.Join(points, ","))
}
