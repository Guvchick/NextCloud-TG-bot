from __future__ import annotations

import sqlite3
from datetime import datetime, timezone
from pathlib import Path

from bot.db import Database


def backup_stamp() -> str:
    return datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")


def create_sqlite_backup(db_path: Path, backup_dir: Path) -> Path:
    backup_dir.mkdir(parents=True, exist_ok=True)
    output_path = backup_dir / f"bot-db-{backup_stamp()}.sqlite3"

    source = sqlite3.connect(db_path)
    try:
        destination = sqlite3.connect(output_path)
        try:
            source.backup(destination)
        finally:
            destination.close()
    finally:
        source.close()

    return output_path


async def create_json_backup(database: Database, backup_dir: Path) -> Path:
    backup_dir.mkdir(parents=True, exist_ok=True)
    output_path = backup_dir / f"bot-users-{backup_stamp()}.json"
    return await database.export_users_json(output_path)
