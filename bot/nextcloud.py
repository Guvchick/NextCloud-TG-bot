from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import aiohttp


class NextcloudError(RuntimeError):
    pass


@dataclass(frozen=True)
class NextcloudCredentials:
    base_url: str
    username: str
    password: str


class NextcloudClient:
    def __init__(self, credentials: NextcloudCredentials) -> None:
        self.credentials = credentials
        self._session: aiohttp.ClientSession | None = None

    async def close(self) -> None:
        if self._session:
            await self._session.close()

    @property
    def session(self) -> aiohttp.ClientSession:
        if self._session is None:
            self._session = aiohttp.ClientSession(
                auth=aiohttp.BasicAuth(self.credentials.username, self.credentials.password),
                headers={"OCS-APIRequest": "true", "Accept": "application/json"},
                raise_for_status=False,
            )
        return self._session

    def _url(self, path: str) -> str:
        return f"{self.credentials.base_url}{path}"

    async def _request(self, method: str, path: str, data: dict[str, str] | None = None) -> dict[str, Any]:
        async with self.session.request(method, self._url(path), data=data) as response:
            text = await response.text()
            try:
                payload = await response.json(content_type=None)
            except Exception as exc:
                raise NextcloudError(f"Nextcloud returned non-JSON response ({response.status}): {text[:300]}") from exc

        if response.status >= 400:
            raise NextcloudError(f"Nextcloud HTTP {response.status}: {text[:300]}")

        meta = payload.get("ocs", {}).get("meta", {})
        status_code = int(meta.get("statuscode", 0))
        if status_code != 100:
            message = meta.get("message") or "unknown Nextcloud OCS error"
            raise NextcloudError(f"Nextcloud OCS {status_code}: {message}")
        return payload.get("ocs", {}).get("data") or {}

    async def user_exists(self, user_id: str) -> bool:
        try:
            await self._request("GET", f"/ocs/v1.php/cloud/users/{user_id}")
            return True
        except NextcloudError as exc:
            if "OCS 101" in str(exc) or "OCS 404" in str(exc):
                return False
            return False

    async def create_user(self, user_id: str, password: str) -> None:
        await self._request(
            "POST",
            "/ocs/v1.php/cloud/users",
            {"userid": user_id, "password": password},
        )

    async def set_user_value(self, user_id: str, key: str, value: str) -> None:
        await self._request(
            "PUT",
            f"/ocs/v1.php/cloud/users/{user_id}",
            {"key": key, "value": value},
        )

    async def ensure_user(self, user_id: str, password: str, quota_gb: int) -> None:
        if await self.user_exists(user_id):
            await self.set_user_value(user_id, "password", password)
        else:
            await self.create_user(user_id, password)
        await self.set_quota(user_id, quota_gb)
        await self.enable_user(user_id)

    async def set_quota(self, user_id: str, quota_gb: int) -> None:
        await self.set_user_value(user_id, "quota", f"{quota_gb} GB")

    async def disable_user(self, user_id: str) -> None:
        await self._request("PUT", f"/ocs/v1.php/cloud/users/{user_id}/disable")

    async def enable_user(self, user_id: str) -> None:
        await self._request("PUT", f"/ocs/v1.php/cloud/users/{user_id}/enable")
