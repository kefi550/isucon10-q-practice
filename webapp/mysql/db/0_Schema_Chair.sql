-- chair は isu2 の MySQL でホストする (estate は isu1、0_Schema_Estate.sql を参照)
DROP DATABASE IF EXISTS isuumo;
CREATE DATABASE isuumo;

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
    -- ORDER BY price ASC, id ASC (getLowPricedChair) をインデックスだけで解決する
    INDEX idx_price_id (price, id),
    -- price の範囲条件 + stock > 0 (searchChairs の SELECT/COUNT) の絞り込みに使う
    INDEX idx_price_stock (price, stock),
    INDEX idx_chair_height (height),
    INDEX idx_chair_width (width),
    INDEX idx_chair_depth (depth),
    INDEX idx_chair_color_popularity_id (color, popularity, id),
    INDEX idx_chair_kind_popularity_id (kind, popularity, id),
    INDEX idx_chair_popularity_id (popularity, id)
);

-- searchChairs の features 絞り込みを正規化テーブル経由で行うための索引テーブル。
-- chair と同じ isu2 の MySQL に置く (estate_feature は isu1 側、0_Schema_Estate.sql を参照)。
CREATE TABLE isuumo.chair_feature
(
    chair_id     INTEGER     NOT NULL,
    feature_value VARCHAR(64) NOT NULL,
    PRIMARY KEY (chair_id, feature_value),
    INDEX idx_chair_feature_value (feature_value, chair_id)
);
