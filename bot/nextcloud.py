from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Any
from urllib.parse import quote
from xml.etree import ElementTree

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

    async def _dav_request(
        self,
        method: str,
        user_id: str,
        password: str,
        remote_path: str = "",
        data: Any = None,
        headers: dict[str, str] | None = None,
    ) -> tuple[int, str]:
        url = self._dav_url(user_id, remote_path)
        async with self.session.request(
            method,
            url,
            data=data,
            headers=headers,
            auth=aiohttp.BasicAuth(user_id, password),
        ) as response:
            text = await response.text()
            return response.status, text

    def _dav_url(self, user_id: str, remote_path: str = "") -> str:
        clean_parts = [
            quote(part, safe="")
            for part in remote_path.strip("/").split("/")
            if part
        ]
        suffix = "/".join(clean_parts)
        base = f"{self.credentials.base_url}/remote.php/dav/files/{quote(user_id, safe='')}"
        return f"{base}/{suffix}" if suffix else f"{base}/"

    async def user_exists(self, user_id: str) -> bool:
        try:
            await self._request("GET", f"/ocs/v1.php/cloud/users/{user_id}")
            return True
        except NextcloudError as exc:
            if "OCS 101" in str(exc) or "OCS 404" in str(exc) or "HTTP 404" in str(exc):
                return False
            raise

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

    async def delete_user(self, user_id: str) -> None:
        await self._request("DELETE", f"/ocs/v1.php/cloud/users/{user_id}")

    async def get_quota(self, user_id: str, password: str) -> dict[str, int | None]:
        body = """<?xml version="1.0"?>
<d:propfind xmlns:d="DAV:">
  <d:prop>
    <d:quota-used-bytes />
    <d:quota-available-bytes />
  </d:prop>
</d:propfind>"""
        status, text = await self._dav_request(
            "PROPFIND",
            user_id,
            password,
            headers={"Depth": "0", "Content-Type": "application/xml", "Accept": "application/xml"},
            data=body,
        )
        if status not in {207, 200}:
            raise NextcloudError(f"Nextcloud WebDAV quota HTTP {status}: {text[:300]}")

        root = ElementTree.fromstring(text)
        used = root.find(".//{DAV:}quota-used-bytes")
        available = root.find(".//{DAV:}quota-available-bytes")
        return {
            "used": int(used.text) if used is not None and used.text else None,
            "available": int(available.text) if available is not None and available.text else None,
        }

    async def ensure_folder(self, user_id: str, password: str, folder: str) -> None:
        status, text = await self._dav_request("MKCOL", user_id, password, folder)
        if status not in {201, 405}:
            raise NextcloudError(f"Nextcloud WebDAV folder HTTP {status}: {text[:300]}")

    async def upload_file(self, user_id: str, password: str, folder: str, filename: str, local_path: Path) -> None:
        await self.ensure_folder(user_id, password, folder)
        remote_path = f"{folder.strip('/')}/{filename}"
        with local_path.open("rb") as file:
            status, text = await self._dav_request("PUT", user_id, password, remote_path, data=file)
        if status not in {200, 201, 204}:
            raise NextcloudError(f"Nextcloud WebDAV upload HTTP {status}: {text[:300]}")
