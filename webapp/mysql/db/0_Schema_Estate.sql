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
    INDEX idx_door_width_height (door_width, door_height),
    INDEX idx_door_height_width (door_height, door_width),
    INDEX idx_rent_id (rent, id),
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
