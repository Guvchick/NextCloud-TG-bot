from __future__ import annotations

import json
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import aiosqlite


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


class Database:
    def __init__(self, path: Path) -> None:
        self.path = path

    async def init(self) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        async with aiosqlite.connect(self.path) as db:
            await db.execute(
                """
                CREATE TABLE IF NOT EXISTS users (
                    telegram_id INTEGER PRIMARY KEY,
                    username TEXT,
                    first_name TEXT,
                    last_name TEXT,
                    status TEXT NOT NULL DEFAULT 'requested',
                    nc_user_id TEXT,
                    quota_gb INTEGER NOT NULL DEFAULT 0,
                    is_disabled INTEGER NOT NULL DEFAULT 0,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    approved_at TEXT
                )
                """
            )
            await db.commit()

    async def upsert_request(
        self,
        telegram_id: int,
        username: str | None,
        first_name: str | None,
        last_name: str | None,
    ) -> dict[str, Any]:
        now = utc_now()
        existing = await self.get_user(telegram_id)
        async with aiosqlite.connect(self.path) as db:
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
                        telegram_id, username, first_name, last_name, status,
                        created_at, updated_at
                    )
                    VALUES (?, ?, ?, ?, 'requested', ?, ?)
                    """,
                    (telegram_id, username, first_name, last_name, now, now),
                )
            await db.commit()
        return await self.get_user(telegram_id) or {}

    async def get_user(self, telegram_id: int) -> dict[str, Any] | None:
        async with aiosqlite.connect(self.path) as db:
            db.row_factory = aiosqlite.Row
            cursor = await db.execute(
                "SELECT * FROM users WHERE telegram_id = ?",
                (telegram_id,),
            )
            row = await cursor.fetchone()
            return dict(row) if row else None

    async def approve_user(self, telegram_id: int, nc_user_id: str, quota_gb: int) -> None:
        now = utc_now()
        async with aiosqlite.connect(self.path) as db:
            await db.execute(
                """
                UPDATE users
                SET status = 'approved',
                    nc_user_id = ?,
                    quota_gb = ?,
                    is_disabled = 0,
                    approved_at = COALESCE(approved_at, ?),
                    updated_at = ?
                WHERE telegram_id = ?
                """,
                (nc_user_id, quota_gb, now, now, telegram_id),
            )
            await db.commit()

    async def reject_user(self, telegram_id: int) -> None:
        now = utc_now()
        async with aiosqlite.connect(self.path) as db:
            await db.execute(
                "UPDATE users SET status = 'rejected', updated_at = ? WHERE telegram_id = ?",
                (now, telegram_id),
            )
            await db.commit()

    async def set_quota(self, telegram_id: int, quota_gb: int) -> None:
        now = utc_now()
        async with aiosqlite.connect(self.path) as db:
            await db.execute(
                "UPDATE users SET quota_gb = ?, updated_at = ? WHERE telegram_id = ?",
                (quota_gb, now, telegram_id),
            )
            await db.commit()

    async def set_disabled(self, telegram_id: int, is_disabled: bool) -> None:
        now = utc_now()
        async with aiosqlite.connect(self.path) as db:
            await db.execute(
                "UPDATE users SET is_disabled = ?, updated_at = ? WHERE telegram_id = ?",
                (1 if is_disabled else 0, now, telegram_id),
            )
            await db.commit()

    async def list_users(self, status: str | None = None, limit: int = 10, offset: int = 0) -> list[dict[str, Any]]:
        async with aiosqlite.connect(self.path) as db:
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
            return [dict(row) for row in rows]

    async def count_users(self, status: str | None = None) -> int:
        async with aiosqlite.connect(self.path) as db:
            if status:
                cursor = await db.execute("SELECT COUNT(*) FROM users WHERE status = ?", (status,))
            else:
                cursor = await db.execute("SELECT COUNT(*) FROM users")
            row = await cursor.fetchone()
            return int(row[0])

    async def approved_telegram_ids(self) -> list[int]:
        async with aiosqlite.connect(self.path) as db:
            cursor = await db.execute(
                "SELECT telegram_id FROM users WHERE status = 'approved' AND is_disabled = 0"
            )
            rows = await cursor.fetchall()
            return [int(row[0]) for row in rows]

    async def export_users_json(self, output_path: Path) -> Path:
        output_path.parent.mkdir(parents=True, exist_ok=True)
        users = await self.list_users(limit=100000, offset=0)
        output_path.write_text(
            json.dumps({"generated_at": utc_now(), "users": users}, ensure_ascii=False, indent=2),
            encoding="utf-8",
        )
        return output_path
