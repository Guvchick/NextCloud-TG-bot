from __future__ import annotations

import gzip
import sqlite3
import shutil
from datetime import datetime, timezone
from pathlib import Path

from bot.db import Database


def backup_stamp() -> str:
    return datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")


def create_sqlite_backup(db_path: Path, backup_dir: Path) -> Path:
    backup_dir.mkdir(parents=True, exist_ok=True)
    raw_path = backup_dir / f"bot-db-{backup_stamp()}.sqlite3"
    output_path = raw_path.with_suffix(raw_path.suffix + ".gz")

    source = sqlite3.connect(db_path)
    try:
        destination = sqlite3.connect(raw_path)
        try:
            source.backup(destination)
        finally:
            destination.close()
    finally:
        source.close()

    with raw_path.open("rb") as raw_file, gzip.open(output_path, "wb") as gzip_file:
        shutil.copyfileobj(raw_file, gzip_file)
    raw_path.unlink(missing_ok=True)
    return output_path


async def create_json_backup(database: Database, backup_dir: Path) -> Path:
    backup_dir.mkdir(parents=True, exist_ok=True)
    raw_path = backup_dir / f"bot-users-{backup_stamp()}.json"
    await database.export_users_json(raw_path)
    output_path = raw_path.with_suffix(raw_path.suffix + ".gz")
    with raw_path.open("rb") as raw_file, gzip.open(output_path, "wb") as gzip_file:
        shutil.copyfileobj(raw_file, gzip_file)
    raw_path.unlink(missing_ok=True)
    return output_path


def restore_sqlite_backup(backup_path: Path, db_path: Path) -> None:
    db_path.parent.mkdir(parents=True, exist_ok=True)
    temp_path = db_path.with_suffix(".restore-tmp.sqlite3")
    if backup_path.suffix == ".gz":
        with gzip.open(backup_path, "rb") as gzip_file, temp_path.open("wb") as temp_file:
            shutil.copyfileobj(gzip_file, temp_file)
    else:
        shutil.copy2(backup_path, temp_path)

    connection = sqlite3.connect(temp_path)
    try:
        result = connection.execute("PRAGMA integrity_check").fetchone()
        if not result or result[0] != "ok":
            raise RuntimeError(f"SQLite backup integrity check failed: {result}")
    finally:
        connection.close()
    temp_path.replace(db_path)


def prune_old_backups(backup_dir: Path, retention_days: int) -> int:
    if not backup_dir.exists():
        return 0
    now = datetime.now(timezone.utc).timestamp()
    cutoff_seconds = retention_days * 24 * 60 * 60
    removed = 0
    for path in backup_dir.glob("bot-*"):
        if path.is_file() and now - path.stat().st_mtime > cutoff_seconds:
            path.unlink(missing_ok=True)
            removed += 1
    return removed


def list_backup_files(backup_dir: Path, limit: int = 10) -> list[Path]:
    if not backup_dir.exists():
        return []
    return sorted(
        [path for path in backup_dir.glob("bot-db-*.sqlite3.gz") if path.is_file()],
        key=lambda path: path.stat().st_mtime,
        reverse=True,
    )[:limit]
