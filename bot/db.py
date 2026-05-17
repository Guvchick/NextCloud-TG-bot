from __future__ import annotations

import base64
import hashlib
import json
from contextlib import asynccontextmanager
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, AsyncIterator

import aiosqlite


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


class Database:
    def __init__(self, path: Path, secret_key: str | None = None, premium_days: int = 30) -> None:
        self.path = path
        self.premium_days = premium_days
        self._fernet = None
        if secret_key:
            try:
                from cryptography.fernet import Fernet
            except ImportError as exc:
                raise RuntimeError("DATABASE_SECRET_KEY requires the cryptography package") from exc
            key = base64.urlsafe_b64encode(hashlib.sha256(secret_key.encode("utf-8")).digest())
            self._fernet = Fernet(key)

    @asynccontextmanager
    async def connection(self) -> AsyncIterator[aiosqlite.Connection]:
        async with aiosqlite.connect(self.path) as db:
            await self._apply_pragmas(db)
            yield db

    async def _apply_pragmas(self, db: aiosqlite.Connection) -> None:
        await db.execute("PRAGMA foreign_keys = ON")
        await db.execute("PRAGMA journal_mode = WAL")
        await db.execute("PRAGMA synchronous = NORMAL")
        await db.execute("PRAGMA busy_timeout = 5000")
        await db.execute("PRAGMA secure_delete = ON")

    def _harden_files(self) -> None:
        for path in (self.path.parent,):
            try:
                path.chmod(0o700)
            except OSError:
                pass
        for path in (self.path, self.path.with_name(f"{self.path.name}-wal"), self.path.with_name(f"{self.path.name}-shm")):
            if path.exists():
                try:
                    path.chmod(0o600)
                except OSError:
                    pass

    def _encrypt_secret(self, value: str | None) -> str | None:
        if not value or not self._fernet or value.startswith("fernet:"):
            return value
        return "fernet:" + self._fernet.encrypt(value.encode("utf-8")).decode("ascii")

    def _decrypt_secret(self, value: str | None) -> str | None:
        if not value or not value.startswith("fernet:"):
            return value
        if not self._fernet:
            raise RuntimeError("DATABASE_SECRET_KEY is required to decrypt stored Nextcloud passwords")
        return self._fernet.decrypt(value.removeprefix("fernet:").encode("ascii")).decode("utf-8")

    def _decode_user(self, row: aiosqlite.Row | None) -> dict[str, Any] | None:
        if not row:
            return None
        user = dict(row)
        user["nc_password"] = self._decrypt_secret(user.get("nc_password"))
        return user

    async def init(self) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._harden_files()
        async with self.connection() as db:
            await db.execute(
                """
                CREATE TABLE IF NOT EXISTS users (
                    telegram_id INTEGER PRIMARY KEY,
                    username TEXT,
                    first_name TEXT,
                    last_name TEXT,
                    status TEXT NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'approved', 'rejected')),
                    language TEXT NOT NULL DEFAULT 'ru' CHECK (language IN ('ru', 'en')),
                    nc_user_id TEXT,
                    nc_password TEXT,
                    quota_gb INTEGER NOT NULL DEFAULT 0 CHECK (quota_gb >= 0),
                    is_supporter INTEGER NOT NULL DEFAULT 0 CHECK (is_supporter IN (0, 1)),
                    supporter_until TEXT,
                    is_disabled INTEGER NOT NULL DEFAULT 0 CHECK (is_disabled IN (0, 1)),
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    approved_at TEXT
                )
                """
            )
            await db.execute(
                """
                CREATE TABLE IF NOT EXISTS settings (
                    key TEXT PRIMARY KEY,
                    value TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                )
                """
            )
            await db.execute(
                """
                CREATE TABLE IF NOT EXISTS payments (
                    transaction_id TEXT PRIMARY KEY,
                    telegram_id INTEGER NOT NULL REFERENCES users(telegram_id) ON DELETE CASCADE,
                    provider TEXT NOT NULL,
                    amount INTEGER NOT NULL CHECK (amount > 0),
                    currency TEXT NOT NULL,
                    status TEXT NOT NULL,
                    payment_url TEXT,
                    payload TEXT,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                )
                """
            )
            await self._ensure_column(db, "users", "nc_password", "TEXT")
            await self._ensure_column(db, "users", "language", "TEXT NOT NULL DEFAULT 'ru'")
            await self._ensure_column(db, "users", "is_supporter", "INTEGER NOT NULL DEFAULT 0")
            await self._ensure_column(db, "users", "supporter_until", "TEXT")
            await self._encrypt_existing_passwords(db)
            await db.commit()
        self._harden_files()

    async def _ensure_column(self, db: aiosqlite.Connection, table: str, column: str, definition: str) -> None:
        cursor = await db.execute(f"PRAGMA table_info({table})")
        columns = {row[1] for row in await cursor.fetchall()}
        if column not in columns:
            await db.execute(f"ALTER TABLE {table} ADD COLUMN {column} {definition}")

    async def _encrypt_existing_passwords(self, db: aiosqlite.Connection) -> None:
        if not self._fernet:
            return
        cursor = await db.execute(
            """
            SELECT telegram_id, nc_password
            FROM users
            WHERE nc_password IS NOT NULL
              AND nc_password != ''
              AND nc_password NOT LIKE 'fernet:%'
            """
        )
        rows = await cursor.fetchall()
        for telegram_id, password in rows:
            await db.execute(
                "UPDATE users SET nc_password = ? WHERE telegram_id = ?",
                (self._encrypt_secret(str(password)), telegram_id),
            )

    async def upsert_request(
        self,
        telegram_id: int,
        username: str | None,
        first_name: str | None,
        last_name: str | None,
    ) -> dict[str, Any]:
        now = utc_now()
        existing = await self.get_user(telegram_id)
        async with self.connection() as db:
            if existing:
                await db.execute(
                    """
                    UPDATE users
                    SET username = ?, first_name = ?, last_name = ?, updated_at = ?
                    WHERE telegram_id = ?
                    """,
                    (username, first_name, last_name, now, telegram_id),
                )
            else:
                await db.execute(
                    """
                    INSERT INTO users (
                        telegram_id, username, first_name, last_name, status, language,
                        created_at, updated_at
                    )
                    VALUES (?, ?, ?, ?, 'requested', 'ru', ?, ?)
                    """,
                    (telegram_id, username, first_name, last_name, now, now),
                )
            await db.commit()
        return await self.get_user(telegram_id) or {}

    async def set_language(self, telegram_id: int, language: str) -> None:
        now = utc_now()
        async with self.connection() as db:
            await db.execute(
                "UPDATE users SET language = ?, updated_at = ? WHERE telegram_id = ?",
                (language, now, telegram_id),
            )
            await db.commit()

    async def get_user(self, telegram_id: int) -> dict[str, Any] | None:
        async with self.connection() as db:
            db.row_factory = aiosqlite.Row
            cursor = await db.execute(
                "SELECT * FROM users WHERE telegram_id = ?",
                (telegram_id,),
            )
            row = await cursor.fetchone()
            return self._decode_user(row)

    async def approve_user(self, telegram_id: int, nc_user_id: str, nc_password: str, quota_gb: int) -> None:
        now = utc_now()
        stored_password = self._encrypt_secret(nc_password)
        async with self.connection() as db:
            await db.execute(
                """
                UPDATE users
                SET status = 'approved',
                    nc_user_id = ?,
                    nc_password = ?,
                    quota_gb = ?,
                    is_disabled = 0,
                    approved_at = COALESCE(approved_at, ?),
                    updated_at = ?
                WHERE telegram_id = ?
                """,
                (nc_user_id, stored_password, quota_gb, now, now, telegram_id),
            )
            await db.commit()

    async def set_nextcloud_password(self, telegram_id: int, nc_password: str) -> None:
        now = utc_now()
        stored_password = self._encrypt_secret(nc_password)
        async with self.connection() as db:
            await db.execute(
                "UPDATE users SET nc_password = ?, updated_at = ? WHERE telegram_id = ?",
                (stored_password, now, telegram_id),
            )
            await db.commit()

    async def reject_user(self, telegram_id: int) -> None:
        now = utc_now()
        async with self.connection() as db:
            await db.execute(
                "UPDATE users SET status = 'rejected', updated_at = ? WHERE telegram_id = ?",
                (now, telegram_id),
            )
            await db.commit()

    async def set_quota(self, telegram_id: int, quota_gb: int) -> None:
        now = utc_now()
        async with self.connection() as db:
            await db.execute(
                "UPDATE users SET quota_gb = ?, updated_at = ? WHERE telegram_id = ?",
                (quota_gb, now, telegram_id),
            )
            await db.commit()

    async def set_disabled(self, telegram_id: int, is_disabled: bool) -> None:
        now = utc_now()
        async with self.connection() as db:
            await db.execute(
                "UPDATE users SET is_disabled = ?, updated_at = ? WHERE telegram_id = ?",
                (1 if is_disabled else 0, now, telegram_id),
            )
            await db.commit()

    async def set_supporter(self, telegram_id: int, is_supporter: bool, supporter_until: str | None = None) -> None:
        now = utc_now()
        async with self.connection() as db:
            await db.execute(
                "UPDATE users SET is_supporter = ?, supporter_until = ?, updated_at = ? WHERE telegram_id = ?",
                (1 if is_supporter else 0, supporter_until if is_supporter else None, now, telegram_id),
            )
            await db.commit()

    async def expire_supporters(self) -> int:
        now = utc_now()
        async with self.connection() as db:
            cursor = await db.execute(
                """
                UPDATE users
                SET is_supporter = 0,
                    supporter_until = NULL,
                    updated_at = ?
                WHERE is_supporter = 1
                  AND supporter_until IS NOT NULL
                  AND supporter_until <= ?
                """,
                (now, now),
            )
            await db.commit()
            return int(cursor.rowcount or 0)

    async def delete_user(self, telegram_id: int) -> None:
        async with self.connection() as db:
            await db.execute("DELETE FROM users WHERE telegram_id = ?", (telegram_id,))
            await db.commit()

    async def approved_users(self) -> list[dict[str, Any]]:
        return await self.list_users(status="approved", limit=100000, offset=0)

    async def get_setting(self, key: str) -> str | None:
        async with self.connection() as db:
            cursor = await db.execute("SELECT value FROM settings WHERE key = ?", (key,))
            row = await cursor.fetchone()
            return str(row[0]) if row else None

    async def set_setting(self, key: str, value: str) -> None:
        now = utc_now()
        async with self.connection() as db:
            await db.execute(
                """
                INSERT INTO settings (key, value, updated_at)
                VALUES (?, ?, ?)
                ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
                """,
                (key, value, now),
            )
            await db.commit()

    async def delete_setting(self, key: str) -> None:
        async with self.connection() as db:
            await db.execute("DELETE FROM settings WHERE key = ?", (key,))
            await db.commit()

    async def list_settings(self, prefix: str | None = None) -> dict[str, str]:
        async with self.connection() as db:
            if prefix:
                cursor = await db.execute("SELECT key, value FROM settings WHERE key LIKE ?", (f"{prefix}%",))
            else:
                cursor = await db.execute("SELECT key, value FROM settings")
            rows = await cursor.fetchall()
            return {str(row[0]): str(row[1]) for row in rows}

    async def create_payment(
        self,
        transaction_id: str,
        telegram_id: int,
        provider: str,
        amount: int,
        currency: str,
        status: str,
        payment_url: str | None = None,
        payload: str | None = None,
    ) -> None:
        now = utc_now()
        async with self.connection() as db:
            await db.execute(
                """
                INSERT INTO payments (
                    transaction_id, telegram_id, provider, amount, currency, status,
                    payment_url, payload, created_at, updated_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(transaction_id) DO UPDATE SET
                    status = excluded.status,
                    payment_url = excluded.payment_url,
                    payload = excluded.payload,
                    updated_at = excluded.updated_at
                """,
                (
                    transaction_id,
                    telegram_id,
                    provider,
                    amount,
                    currency,
                    status,
                    payment_url,
                    payload,
                    now,
                    now,
                ),
            )
            await db.commit()

    async def get_payment(self, transaction_id: str) -> dict[str, Any] | None:
        async with self.connection() as db:
            db.row_factory = aiosqlite.Row
            cursor = await db.execute(
                "SELECT * FROM payments WHERE transaction_id = ?",
                (transaction_id,),
            )
            row = await cursor.fetchone()
            return dict(row) if row else None

    async def update_payment_status(self, transaction_id: str, status: str) -> None:
        now = utc_now()
        async with self.connection() as db:
            await db.execute(
                "UPDATE payments SET status = ?, updated_at = ? WHERE transaction_id = ?",
                (status, now, transaction_id),
            )
            await db.commit()

    async def list_users(self, status: str | None = None, limit: int = 10, offset: int = 0) -> list[dict[str, Any]]:
        async with self.connection() as db:
            db.row_factory = aiosqlite.Row
            if status:
                cursor = await db.execute(
                    """
                    SELECT * FROM users
                    WHERE status = ?
                    ORDER BY created_at DESC
                    LIMIT ? OFFSET ?
                    """,
                    (status, limit, offset),
                )
            else:
                cursor = await db.execute(
                    """
                    SELECT * FROM users
                    ORDER BY created_at DESC
                    LIMIT ? OFFSET ?
                    """,
                    (limit, offset),
            )
            rows = await cursor.fetchall()
            return [self._decode_user(row) or {} for row in rows]

    async def count_users(self, status: str | None = None) -> int:
        async with self.connection() as db:
            if status:
                cursor = await db.execute("SELECT COUNT(*) FROM users WHERE status = ?", (status,))
            else:
                cursor = await db.execute("SELECT COUNT(*) FROM users")
            row = await cursor.fetchone()
            return int(row[0])

    async def approved_telegram_ids(self) -> list[int]:
        async with self.connection() as db:
            cursor = await db.execute(
                "SELECT telegram_id FROM users WHERE status = 'approved' AND is_disabled = 0"
            )
            rows = await cursor.fetchall()
            return [int(row[0]) for row in rows]

    async def export_users_json(self, output_path: Path) -> Path:
        output_path.parent.mkdir(parents=True, exist_ok=True)
        users = await self.list_users(limit=100000, offset=0)
        for user in users:
            user.pop("nc_password", None)
        output_path.write_text(
            json.dumps({"generated_at": utc_now(), "users": users}, ensure_ascii=False, indent=2),
            encoding="utf-8",
        )
        return output_path
