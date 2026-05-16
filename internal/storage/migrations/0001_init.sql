CREATE TABLE IF NOT EXISTS observation (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    datetime          TEXT    NOT NULL,
    location          INTEGER NOT NULL,
    mslp              REAL,
    rh                REAL,
    temperature       REAL,
    water_temperature REAL,
    wind_avg          REAL    NOT NULL,
    wind_direction    INTEGER NOT NULL,
    wind_max          REAL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_observation_datetime_location
    ON observation (datetime, location);
