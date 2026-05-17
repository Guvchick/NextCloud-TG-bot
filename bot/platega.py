from __future__ import annotations

import logging
from typing import Any

import aiohttp


class PlategaError(RuntimeError):
    pass


class PlategaClient:
    def __init__(self, merchant_id: str, secret: str, base_url: str = "https://app.platega.io") -> None:
        self.merchant_id = merchant_id
        self.secret = secret
        self.base_url = base_url.rstrip("/")
        self._session: aiohttp.ClientSession | None = None

    @property
    def session(self) -> aiohttp.ClientSession:
        if self._session is None:
            self._session = aiohttp.ClientSession(
                headers={
                    "X-MerchantId": self.merchant_id,
                    "X-Secret": self.secret,
                    "Accept": "application/json",
                    "Content-Type": "application/json",
                    "User-Agent": "telegram-nextcloud-bot/1.0",
                },
                raise_for_status=False,
            )
        return self._session

    async def close(self) -> None:
        if self._session:
            await self._session.close()

    async def _json_request(self, method: str, path: str, **kwargs) -> dict[str, Any]:
        url = f"{self.base_url}{path}"
        async with self.session.request(method, url, **kwargs) as response:
            text = await response.text()
            try:
                payload = await response.json(content_type=None)
            except Exception as exc:
                raise PlategaError(f"Platega returned non-JSON response ({response.status}): {text[:300]}") from exc
        if response.status >= 400:
            raise PlategaError(f"Platega HTTP {response.status}: {text[:300]}")
        if not isinstance(payload, dict):
            raise PlategaError("Platega response is not an object")
        return payload

    async def create_payment_link(
        self,
        amount_rub: int,
        description: str,
        payload: str,
        return_url: str | None = None,
        failed_url: str | None = None,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {
            "paymentDetails": {"amount": amount_rub, "currency": "RUB"},
            "description": description,
            "payload": payload,
        }
        if return_url:
            body["return"] = return_url
        if failed_url:
            body["failedUrl"] = failed_url
        data = await self._json_request("POST", "/v2/transaction/process", json=body)
        if not data.get("transactionId") or not data.get("url"):
            logging.warning("Unexpected Platega create response: %s", data)
            raise PlategaError("Platega response does not contain transactionId or url")
        return data

    async def get_transaction(self, transaction_id: str) -> dict[str, Any]:
        return await self._json_request("GET", f"/transaction/{transaction_id}")
