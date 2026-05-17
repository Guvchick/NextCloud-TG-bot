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
    nextcloud_internal_url: str
    nextcloud_admin_user: str
    nextcloud_admin_password: str
    default_quota_gb: int
    database_path: Path
    database_url: str
    database_api_token: str | None
    database_secret_key: str | None
    backup_dir: Path
    log_dir: Path
    upload_folder: str
    enable_support_block: bool
    support_telegram: str | None
    support_email: str | None
    enable_donate_block: bool
    donate_url: str | None
    telegram_stars_enabled: bool
    telegram_stars_amounts: tuple[int, ...]
    platega_enabled: bool
    platega_url: str | None
    platega_merchant_id: str | None
    platega_secret: str | None
    platega_base_url: str
    platega_amounts_rub: tuple[int, ...]
    platega_return_url: str | None
    platega_failed_url: str | None
    backup_retention_days: int
    auto_backup_interval_hours: int
    nextcloud_sync_interval_minutes: int
    telegram_max_download_mb: int
    premium_days: int
    sticker_welcome: str | None
    sticker_approved: str | None
    sticker_upload_ok: str | None
    sticker_error: str | None


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


def _optional(name: str) -> str | None:
    value = os.getenv(name, "").strip()
    return value or None


def _int_env(name: str, default: int, minimum: int = 1) -> int:
    value = int(os.getenv(name, str(default)))
    if value < minimum:
        raise RuntimeError(f"{name} must be at least {minimum}")
    return value


def _bool_env(name: str, default: bool = True) -> bool:
    raw = os.getenv(name, "").strip().lower()
    if not raw:
        return default
    return raw in {"1", "true", "yes", "y", "on", "да"}


def _int_tuple_env(name: str, default: tuple[int, ...]) -> tuple[int, ...]:
    raw = os.getenv(name, "").strip()
    if not raw:
        return default
    values: list[int] = []
    for item in raw.split(","):
        item = item.strip()
        if not item:
            continue
        value = int(item)
        if value <= 0:
            raise RuntimeError(f"{name} values must be greater than zero")
        values.append(value)
    return tuple(values) or default


def load_config() -> Config:
    load_dotenv()

    default_quota_gb = int(os.getenv("DEFAULT_QUOTA_GB", "10"))
    if default_quota_gb <= 0:
        raise RuntimeError("DEFAULT_QUOTA_GB must be greater than zero")

    nextcloud_url = _required("NEXTCLOUD_URL").rstrip("/")
    nextcloud_internal_url = os.getenv("NEXTCLOUD_INTERNAL_URL", "").strip().rstrip("/") or nextcloud_url

    return Config(
        bot_token=_required("BOT_TOKEN"),
        admin_ids=_admin_ids(_required("ADMIN_IDS")),
        nextcloud_url=nextcloud_url,
        nextcloud_internal_url=nextcloud_internal_url,
        nextcloud_admin_user=_required("NEXTCLOUD_ADMIN_USER"),
        nextcloud_admin_password=_required("NEXTCLOUD_ADMIN_PASSWORD"),
        default_quota_gb=default_quota_gb,
        database_path=Path(os.getenv("DATABASE_PATH", "data/bot.sqlite3")),
        database_url=os.getenv("DATABASE_URL", "http://bot-db:8080").strip().rstrip("/"),
        database_api_token=_optional("DATABASE_API_TOKEN"),
        database_secret_key=_optional("DATABASE_SECRET_KEY"),
        backup_dir=Path(os.getenv("BACKUP_DIR", "backups")),
        log_dir=Path(os.getenv("LOG_DIR", "logs")),
        upload_folder=os.getenv("UPLOAD_FOLDER", "Telegram uploads").strip() or "Telegram uploads",
        enable_support_block=_bool_env("ENABLE_SUPPORT_BLOCK", True),
        support_telegram=_optional("SUPPORT_TELEGRAM"),
        support_email=_optional("SUPPORT_EMAIL"),
        enable_donate_block=_bool_env("ENABLE_DONATE_BLOCK", True),
        donate_url=_optional("DONATE_URL"),
        telegram_stars_enabled=_bool_env("TELEGRAM_STARS_ENABLED", True),
        telegram_stars_amounts=_int_tuple_env("TELEGRAM_STARS_AMOUNTS", (50, 100, 250)),
        platega_enabled=_bool_env("PLATEGA_ENABLED", True),
        platega_url=_optional("PLATEGA_URL"),
        platega_merchant_id=_optional("PLATEGA_MERCHANT_ID"),
        platega_secret=_optional("PLATEGA_SECRET"),
        platega_base_url=os.getenv("PLATEGA_BASE_URL", "https://app.platega.io").strip().rstrip("/"),
        platega_amounts_rub=_int_tuple_env("PLATEGA_AMOUNTS_RUB", (100, 300, 500)),
        platega_return_url=_optional("PLATEGA_RETURN_URL"),
        platega_failed_url=_optional("PLATEGA_FAILED_URL"),
        backup_retention_days=_int_env("BACKUP_RETENTION_DAYS", 7),
        auto_backup_interval_hours=_int_env("AUTO_BACKUP_INTERVAL_HOURS", 24),
        nextcloud_sync_interval_minutes=_int_env("NEXTCLOUD_SYNC_INTERVAL_MINUTES", 60),
        telegram_max_download_mb=_int_env("TELEGRAM_MAX_DOWNLOAD_MB", 20),
        premium_days=_int_env("PREMIUM_DAYS", 30),
        sticker_welcome=_optional("STICKER_WELCOME"),
        sticker_approved=_optional("STICKER_APPROVED"),
        sticker_upload_ok=_optional("STICKER_UPLOAD_OK"),
        sticker_error=_optional("STICKER_ERROR"),
    )
