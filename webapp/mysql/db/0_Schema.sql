DROP DATABASE IF EXISTS isuumo;
CREATE DATABASE isuumo;

DROP TABLE IF EXISTS isuumo.estate;
DROP TABLE IF EXISTS isuumo.chair;
DROP TABLE IF EXISTS isuumo.estate_feature;
DROP TABLE IF EXISTS isuumo.chair_feature;

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
    INDEX idx_door_width_height (door_width, door_height),
    INDEX idx_door_height_width (door_height, door_width),
    INDEX idx_rent_id (rent, id),
    INDEX idx_estate_popularity_id (popularity, id),
    INDEX idx_estate_latitude_longitude (latitude, longitude)
);

CREATE TABLE isuumo.chair
(
    id          INTEGER         NOT NULL PRIMARY KEY,
    name        VARCHAR(64)     NOT NULL,
    description VARCHAR(4096)   NOT NULL,
    thumbnail   VARCHAR(128)    NOT NULL,
    price       INTEGER         NOT NULL,
    height      INTEGER         NOT NULL,
    width       INTEGER         NOT NULL,
    depth       INTEGER         NOT NULL,
    color       VARCHAR(64)     NOT NULL,
    features    VARCHAR(64)     NOT NULL,
    kind        VARCHAR(64)     NOT NULL,
    popularity  INTEGER         NOT NULL,
    stock       INTEGER         NOT NULL,
    INDEX idx_price_id (price, id),
    INDEX idx_price_stock (price, stock),
    INDEX idx_chair_height (height),
    INDEX idx_chair_width (width),
    INDEX idx_chair_depth (depth),
    INDEX idx_chair_color_popularity_id (color, popularity, id),
    INDEX idx_chair_kind_popularity_id (kind, popularity, id),
    INDEX idx_chair_popularity_id (popularity, id)
);

CREATE TABLE isuumo.chair_feature
(
    chair_id     INTEGER     NOT NULL,
    feature_value VARCHAR(64) NOT NULL,
    PRIMARY KEY (chair_id, feature_value),
    INDEX idx_chair_feature_value (feature_value, chair_id)
);

CREATE TABLE isuumo.estate_feature
(
    estate_id    INTEGER     NOT NULL,
    feature_value VARCHAR(64) NOT NULL,
    PRIMARY KEY (estate_id, feature_value),
    INDEX idx_estate_feature_value (feature_value, estate_id)
);
