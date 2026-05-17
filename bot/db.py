from __future__ import annotations

import json
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import aiohttp


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


class DatabaseError(RuntimeError):
    pass


class Database:
    def __init__(
        self,
        path: Path,
        service_url: str = "http://bot-db:8080",
        api_token: str | None = None,
        secret_key: str | None = None,
        premium_days: int = 30,
    ) -> None:
        self.path = path
        self.service_url = service_url.rstrip("/")
        self.api_token = api_token
        self.secret_key = secret_key
        self.premium_days = premium_days
        self._session: aiohttp.ClientSession | None = None

    @property
    def session(self) -> aiohttp.ClientSession:
        if self._session is None:
            headers = {"Accept": "application/json", "Content-Type": "application/json"}
            if self.api_token:
                headers["Authorization"] = f"Bearer {self.api_token}"
            self._session = aiohttp.ClientSession(headers=headers, raise_for_status=False)
        return self._session

    async def close(self) -> None:
        if self._session:
            await self._session.close()

    async def _rpc(self, method: str, **params) -> Any:
        async with self.session.post(f"{self.service_url}/rpc", json={"method": method, "params": params}) as response:
            text = await response.text()
            try:
                payload = await response.json(content_type=None)
            except Exception as exc:
                raise DatabaseError(f"Go DB returned non-JSON response ({response.status}): {text[:300]}") from exc
        if response.status >= 400 or not payload.get("ok", False):
            raise DatabaseError(str(payload.get("error") or text[:300]))
        return payload.get("data")

    async def init(self) -> None:
        await self._rpc("init")

    async def upsert_request(
        self,
        telegram_id: int,
        username: str | None,
        first_name: str | None,
        last_name: str | None,
    ) -> dict[str, Any]:
        return await self._rpc(
            "upsert_request",
            telegram_id=telegram_id,
            username=username,
            first_name=first_name,
            last_name=last_name,
        ) or {}

    async def set_language(self, telegram_id: int, language: str) -> None:
        await self._rpc("set_language", telegram_id=telegram_id, language=language)

    async def get_user(self, telegram_id: int) -> dict[str, Any] | None:
        return await self._rpc("get_user", telegram_id=telegram_id)

    async def approve_user(self, telegram_id: int, nc_user_id: str, nc_password: str, quota_gb: int) -> None:
        await self._rpc(
            "approve_user",
            telegram_id=telegram_id,
            nc_user_id=nc_user_id,
            nc_password=nc_password,
            quota_gb=quota_gb,
        )

    async def set_nextcloud_password(self, telegram_id: int, nc_password: str) -> None:
        await self._rpc("set_nextcloud_password", telegram_id=telegram_id, nc_password=nc_password)

    async def reject_user(self, telegram_id: int) -> None:
        await self._rpc("reject_user", telegram_id=telegram_id)

    async def set_quota(self, telegram_id: int, quota_gb: int) -> None:
        await self._rpc("set_quota", telegram_id=telegram_id, quota_gb=quota_gb)

    async def set_disabled(self, telegram_id: int, is_disabled: bool) -> None:
        await self._rpc("set_disabled", telegram_id=telegram_id, is_disabled=is_disabled)

    async def set_supporter(self, telegram_id: int, is_supporter: bool, supporter_until: str | None = None) -> None:
        await self._rpc(
            "set_supporter",
            telegram_id=telegram_id,
            is_supporter=is_supporter,
            supporter_until=supporter_until,
        )

    async def expire_supporters(self) -> int:
        return int(await self._rpc("expire_supporters") or 0)

    async def delete_user(self, telegram_id: int) -> None:
        await self._rpc("delete_user", telegram_id=telegram_id)

    async def approved_users(self) -> list[dict[str, Any]]:
        return await self._rpc("approved_users") or []

    async def get_setting(self, key: str) -> str | None:
        return await self._rpc("get_setting", key=key)

    async def set_setting(self, key: str, value: str) -> None:
        await self._rpc("set_setting", key=key, value=value)

    async def delete_setting(self, key: str) -> None:
        await self._rpc("delete_setting", key=key)

    async def list_settings(self, prefix: str | None = None) -> dict[str, str]:
        return await self._rpc("list_settings", prefix=prefix) or {}

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
        await self._rpc(
            "create_payment",
            transaction_id=transaction_id,
            telegram_id=telegram_id,
            provider=provider,
            amount=amount,
            currency=currency,
            status=status,
            payment_url=payment_url,
            payload=payload,
        )

    async def get_payment(self, transaction_id: str) -> dict[str, Any] | None:
        return await self._rpc("get_payment", transaction_id=transaction_id)

    async def update_payment_status(self, transaction_id: str, status: str) -> None:
        await self._rpc("update_payment_status", transaction_id=transaction_id, status=status)

    async def list_users(self, status: str | None = None, limit: int = 10, offset: int = 0) -> list[dict[str, Any]]:
        return await self._rpc("list_users", status=status, limit=limit, offset=offset) or []

    async def count_users(self, status: str | None = None) -> int:
        return int(await self._rpc("count_users", status=status) or 0)

    async def approved_telegram_ids(self) -> list[int]:
        return [int(item) for item in (await self._rpc("approved_telegram_ids") or [])]

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
