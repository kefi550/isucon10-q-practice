-- estate は isu1 の MySQL でホストする (chair は isu2、0_Schema_Chair.sql を参照)
DROP DATABASE IF EXISTS isuumo;
CREATE DATABASE isuumo;

CREATE TABLE isuumo.estate
(
    id          INTEGER             NOT NULL PRIMARY KEY,
    name        VARCHAR(64)         NOT NULL,
    description VARCHAR(4096)       NOT NULL,
    thumbnail   VARCHAR(128)        NOT NULL,
    address     VARCHAR(128)        NOT NULL,
    latitude    DOUBLE PRECISION    NOT NULL,
    longitude   DOUBLE PRECISION    NOT NULL,
    rent        INTEGER             NOT NULL,
    door_height INTEGER             NOT NULL,
    door_width  INTEGER             NOT NULL,
    features    VARCHAR(64)         NOT NULL,
    popularity  INTEGER             NOT NULL,
    -- See the chair schema for why this generated sort key is necessary on
    -- MySQL 5.7.  It preserves popularity DESC, id ASC exactly.
    popularity_desc INTEGER GENERATED ALWAYS AS (-popularity) STORED,
    -- The search fixture divides these values into fixed, non-overlapping
    -- buckets. Equality predicates on the generated IDs let MySQL use every
    -- range dimension and then continue scanning in response order.
    door_height_range_id TINYINT GENERATED ALWAYS AS
        (CASE WHEN door_height < 80 THEN 0 WHEN door_height < 110 THEN 1 WHEN door_height < 150 THEN 2 ELSE 3 END) STORED,
    door_width_range_id TINYINT GENERATED ALWAYS AS
        (CASE WHEN door_width < 80 THEN 0 WHEN door_width < 110 THEN 1 WHEN door_width < 150 THEN 2 ELSE 3 END) STORED,
    rent_range_id TINYINT GENERATED ALWAYS AS
        (CASE WHEN rent < 50000 THEN 0 WHEN rent < 100000 THEN 1 WHEN rent < 150000 THEN 2 ELSE 3 END) STORED,
    -- nazotte 検索 (searchEstateNazotte) 用。緯度経度の bounding box を B-tree の
    -- (latitude, longitude) 範囲スキャン + ICP で絞るより、R-tree の SPATIAL INDEX で
    -- 2次元同時に絞り込む方が候補行数を減らせる。ST_Contains によるポリゴン厳密判定は
    -- そのまま残し、この列は事前の bounding box 絞り込みにのみ使う。
    location POINT GENERATED ALWAYS AS (Point(latitude, longitude)) STORED NOT NULL,
    SPATIAL INDEX idx_estate_location (location),
    INDEX idx_door_width_height (door_width, door_height),
    INDEX idx_door_height_width (door_height, door_width),
    INDEX idx_rent_id (rent, id),
    INDEX idx_estate_height_width_rent_popularity
        (door_height_range_id, door_width_range_id, rent_range_id, popularity_desc, id),
    INDEX idx_estate_width_rent_popularity
        (door_width_range_id, rent_range_id, popularity_desc, id),
    INDEX idx_estate_height_rent_popularity
        (door_height_range_id, rent_range_id, popularity_desc, id),
    INDEX idx_estate_popularity_id (popularity_desc, id),
    INDEX idx_estate_latitude_longitude (latitude, longitude)
);

-- searchEstates の features 絞り込みを正規化テーブル経由で行うための索引テーブル。
-- estate と同じ isu1 の MySQL に置く (chair_feature は isu2 側、0_Schema_Chair.sql を参照)。
CREATE TABLE isuumo.estate_feature
(
    estate_id    INTEGER     NOT NULL,
    feature_value VARCHAR(64) NOT NULL,
    PRIMARY KEY (estate_id, feature_value),
    INDEX idx_estate_feature_value (feature_value, estate_id)
);
