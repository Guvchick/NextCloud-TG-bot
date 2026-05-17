from __future__ import annotations

import logging

import aiohttp


class BoostyError(RuntimeError):
    pass


class BoostyClient:
    def __init__(self, access_token: str, subscribers_url: str) -> None:
        self.access_token = access_token
        self.subscribers_url = subscribers_url
        self._session: aiohttp.ClientSession | None = None

    @property
    def session(self) -> aiohttp.ClientSession:
        if self._session is None:
            self._session = aiohttp.ClientSession(
                headers={
                    "Authorization": f"Bearer {self.access_token}",
                    "Accept": "application/json",
                    "User-Agent": "telegram-nextcloud-bot/1.0",
                },
                raise_for_status=False,
            )
        return self._session

    async def close(self) -> None:
        if self._session:
            await self._session.close()

    async def fetch_active_emails(self) -> set[str]:
        emails: set[str] = set()
        offset = 0
        limit = 100

        while True:
            params = {
                "sort_by": "on_time",
                "offset": str(offset),
                "limit": str(limit),
                "order": "gt",
            }
            async with self.session.get(self.subscribers_url, params=params) as response:
                text = await response.text()
                if response.status >= 400:
                    raise BoostyError(f"Boosty HTTP {response.status}: {text[:300]}")
                try:
                    payload = await response.json(content_type=None)
                except Exception as exc:
                    raise BoostyError(f"Boosty returned non-JSON response: {text[:300]}") from exc

            items = self._subscriber_items(payload)
            if not isinstance(items, list):
                raise BoostyError("Boosty response does not contain a subscriber list")

            for item in items:
                email = self._email_from(item)
                if email and self._looks_active(item):
                    emails.add(email)

            logging.info("Boosty subscribers fetched page: offset=%s count=%s", offset, len(items))
            if len(items) < limit:
                break
            offset += limit

        return emails

    def _subscriber_items(self, payload: object) -> list[dict]:
        if isinstance(payload, list):
            return [item for item in payload if isinstance(item, dict)]
        if not isinstance(payload, dict):
            return []
        for key in ("data", "subscribers", "items", "users"):
            value = payload.get(key)
            if isinstance(value, list):
                return [item for item in value if isinstance(item, dict)]
        return []

    def _email_from(self, item: dict) -> str | None:
        candidates: list[object] = [
            item.get("email"),
            item.get("payerEmail"),
            item.get("payer_email"),
            item.get("boostyEmail"),
            item.get("boosty_email"),
        ]
        user = item.get("user")
        if isinstance(user, dict):
            candidates.extend([user.get("email"), user.get("payerEmail"), user.get("boostyEmail")])
        for candidate in candidates:
            email = str(candidate or "").strip().lower()
            if "@" in email:
                return email
        return None

    def _looks_active(self, item: dict) -> bool:
        if bool(item.get("isBlackListed") or item.get("is_blacklisted") or item.get("blacklisted")):
            return False
        for key in ("subscribed", "isActive", "is_active", "active", "paid"):
            if key in item:
                return bool(item.get(key))
        subscription = item.get("subscription")
        if isinstance(subscription, dict):
            for key in ("subscribed", "isActive", "is_active", "active", "paid"):
                if key in subscription:
                    return bool(subscription.get(key))
        level = item.get("level")
        if isinstance(level, dict):
            return True
        return True
