# Repository Guidelines

## Project Structure & Module Organization

- `webapp/go/` contains the Go 1.14 Echo application (`main.go`), module files, `Makefile`, and `Dockerfile`.
- `webapp/mysql/db/` contains the schema and database initialization script. Dummy-data SQL files and generated database data are ignored; obtain them from the ISUCON environment when needed.
- `isu1/etc/` mirrors service configuration for the benchmark host, including systemd, nginx, MySQL, and logrotate files.
- `env.sh` provides runtime defaults. `pull.sh` synchronizes app and host configuration from `isu1`; `bench.sh` runs the benchmark through the `bench` SSH host.

## Build, Test, and Development Commands

From the repository root, build the binary with:

```sh
make -C webapp/go
```

Run it from `webapp/go/` so its relative fixture paths resolve correctly:

```sh
cd webapp/go && ./isuumo
```

The service listens on port 1323 by default and expects a reachable MySQL `isuumo` database. Override settings with `MYSQL_HOST`, `MYSQL_PORT`, `MYSQL_USER`, `MYSQL_DBNAME`, `MYSQL_PASS`, and `SERVER_PORT`. With dummy-data files present, initialize the database using `webapp/mysql/db/init.sh`. Use `./bench.sh` after the `isu1` and `bench` SSH hosts are configured.

## Coding Style & Naming Conventions

Run `gofmt` on changed Go files; use Go’s tab-based formatting, PascalCase for exported identifiers, and camelCase for unexported names. Follow the handler naming pattern such as `getChairDetail`. Keep SQL values parameterized and deployment settings in environment variables. No lint configuration is provided.

## Testing Guidelines

There are no checked-in tests or coverage thresholds. Add Go tests as `*_test.go` files with `TestXxx` names and run them from `webapp/go/` with `go test ./...`. For handler changes, cover valid requests, malformed parameters, missing records, and database errors; also smoke-test the API against a seeded database.

## Commit & Pull Request Guidelines

The short history uses concise subjects, including a lowercase type prefix such as `feature: add bench script`, alongside short Japanese summaries. Keep commits focused and use `<type>: <short imperative description>` when applicable (for example, `fix: handle empty search filters`). PRs should explain behavior or performance impact, list validation commands and benchmark results when relevant, and call out changed host or database configuration. Include screenshots only for user-visible changes and link a related issue when available.

## Security & Configuration Tips

Treat `env.sh` and SSH configuration as local infrastructure settings; do not add real credentials or private keys. Review `pull.sh` targets before syncing, and keep generated or environment-specific data out of commits.
