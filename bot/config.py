from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path

from dotenv import load_dotenv


@dataclass(frozen=True)
class Config:
    bot_token: str
    admin_ids: set[int]
    nextcloud_url: str
    nextcloud_admin_user: str
    nextcloud_admin_password: str
    default_quota_gb: int
    database_path: Path
    backup_dir: Path


def _required(name: str) -> str:
    value = os.getenv(name, "").strip()
    if not value:
        raise RuntimeError(f"Missing required environment variable: {name}")
    return value


def _admin_ids(raw: str) -> set[int]:
    ids: set[int] = set()
    for item in raw.split(","):
        item = item.strip()
        if item:
            ids.add(int(item))
    if not ids:
        raise RuntimeError("ADMIN_IDS must contain at least one Telegram user id")
    return ids


def load_config() -> Config:
    load_dotenv()

    default_quota_gb = int(os.getenv("DEFAULT_QUOTA_GB", "10"))
    if default_quota_gb <= 0:
        raise RuntimeError("DEFAULT_QUOTA_GB must be greater than zero")

    return Config(
        bot_token=_required("BOT_TOKEN"),
        admin_ids=_admin_ids(_required("ADMIN_IDS")),
        nextcloud_url=_required("NEXTCLOUD_URL").rstrip("/"),
        nextcloud_admin_user=_required("NEXTCLOUD_ADMIN_USER"),
        nextcloud_admin_password=_required("NEXTCLOUD_ADMIN_PASSWORD"),
        default_quota_gb=default_quota_gb,
        database_path=Path(os.getenv("DATABASE_PATH", "data/bot.sqlite3")),
        backup_dir=Path(os.getenv("BACKUP_DIR", "backups")),
    )
