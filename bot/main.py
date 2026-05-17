from __future__ import annotations

import asyncio
import html
import itertools
import logging
import re
import secrets
import string
import tempfile
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from logging.handlers import RotatingFileHandler
from pathlib import Path

from aiogram import Bot, Dispatcher, F, Router
from aiogram.client.default import DefaultBotProperties
from aiogram.enums import ParseMode
from aiogram.exceptions import TelegramBadRequest
from aiogram.filters import Command, CommandStart, StateFilter
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from aiogram.fsm.storage.memory import MemoryStorage
from aiogram.types import CallbackQuery, FSInputFile, LabeledPrice, Message, PreCheckoutQuery

from bot.backups import (
    create_json_backup,
    create_sqlite_backup,
    list_backup_files,
    prune_old_backups,
    restore_sqlite_backup,
)
from bot.config import Config, load_config
from bot.db import Database
from bot.keyboards import (
    account_back_keyboard,
    account_keyboard,
    admin_keyboard,
    backup_keyboard,
    broadcast_confirm_keyboard,
    delete_confirm_keyboard,
    donate_keyboard,
    stars_amounts_keyboard,
    platega_amounts_keyboard,
    platega_payment_keyboard,
    request_review_keyboard,
    restore_backup_keyboard,
    language_keyboard,
    support_keyboard,
    user_keyboard,
    users_keyboard,
)
from bot.nextcloud import NextcloudClient, NextcloudCredentials, NextcloudError
from bot.platega import PlategaClient, PlategaError

router = Router()
PAGE_SIZE = 8
UPLOAD_QUEUE_COUNTER = itertools.count()


@dataclass(frozen=True)
class UploadJob:
    telegram_id: int
    chat_id: int
    status_message_id: int
    file_id: str
    filename: str
    file_size: int
    lang: str
    is_supporter: bool


class BroadcastState(StatesGroup):
    waiting_content = State()
    confirming = State()


class QuotaState(StatesGroup):
    waiting_amount = State()


class UserPasswordState(StatesGroup):
    waiting_password = State()


class StickerState(StatesGroup):
    waiting_sticker = State()


def is_admin(user_id: int, config: Config) -> bool:
    return user_id in config.admin_ids


def display_name(user: dict) -> str:
    if user.get("username"):
        return f"@{html.escape(user['username'])}"
    parts = [user.get("first_name"), user.get("last_name")]
    name = " ".join(part for part in parts if part)
    return html.escape(name or str(user["telegram_id"]))


def generate_password(length: int = 18) -> str:
    alphabet = string.ascii_letters + string.digits + "_-"
    return "".join(secrets.choice(alphabet) for _ in range(length))


def valid_password(password: str) -> bool:
    return len(password) >= 8 and len(password) <= 128 and not password.isspace()


def premium_until(config: Config) -> str:
    return (datetime.now(timezone.utc) + timedelta(days=config.premium_days)).isoformat(timespec="seconds")


def is_premium(user: dict | None) -> bool:
    if not user or not user.get("is_supporter"):
        return False
    until = user.get("supporter_until")
    if not until:
        return False
    try:
        expires_at = datetime.fromisoformat(str(until))
    except ValueError:
        return False
    return expires_at > datetime.now(timezone.utc)


def premium_until_text(user: dict) -> str:
    until = user.get("supporter_until")
    if not until:
        return "-"
    try:
        return datetime.fromisoformat(str(until)).strftime("%Y-%m-%d")
    except ValueError:
        return str(until)


TEXT = {
    "ru": {
        "account_title": "Ваше облако",
        "server": "Сервер",
        "login": "Логин",
        "password": "Пароль",
        "password_missing": "не сохранен",
        "quota": "Квота",
        "upload_hint": "Отправьте файл в этот чат, и бот загрузит его в облако.",
        "support_title": "Поддержка",
        "support_empty": "Контакты поддержки пока не настроены.",
        "support_disabled": "Раздел поддержки отключен.",
        "donate_title": "Поддержать проект",
        "donate_empty": "Способы поддержки пока не настроены.",
        "donate_text": "Можно поддержать проект через Telegram Stars или Platega.",
        "donate_disabled": "Раздел доната отключен.",
        "donate_choose": "Выберите способ поддержки.",
        "stars_title": "Telegram Stars",
        "stars_text": "Выберите сумму в звездах.",
        "stars_invoice_title": "Поддержка проекта",
        "stars_invoice_description": "Спасибо за поддержку проекта!",
        "stars_paid": "Спасибо за поддержку! Премиум-иконка активирована.",
        "platega_title": "Platega",
        "platega_text": "Выберите сумму в рублях. Бот создаст платежную ссылку Platega.",
        "platega_disabled": "Platega отключена.",
        "platega_not_configured": "Platega не настроена: заполните PLATEGA_MERCHANT_ID и PLATEGA_SECRET или задайте PLATEGA_URL.",
        "platega_created": "Платеж создан. После оплаты нажмите «Проверить оплату».",
        "platega_pending": "Платеж пока не подтвержден. Статус: {status}",
        "platega_paid": "Оплата подтверждена! Премиум-иконка активирована.",
        "platega_failed": "Платеж не активен. Статус: {status}",
        "platega_create_failed": "Не удалось создать платеж Platega",
        "platega_check_failed": "Не удалось проверить платеж Platega",
        "premium_badge": "Премиум-поддержка",
        "language_title": "Выберите язык",
        "language_saved": "Язык сохранен.",
        "change_password_prompt": "Отправьте новый пароль для облака.\n\nМинимум 8 символов. После смены бот обновит сохраненный пароль для загрузок.",
        "password_invalid": "Пароль должен быть от 8 до 128 символов.",
        "password_changed": "Пароль сменен.",
        "password_change_failed": "Не удалось сменить пароль",
        "access_inactive": "Доступ не активен.",
        "upload_not_allowed": "Загрузка доступна только одобренным активным пользователям.",
        "webdav_password_missing": "Для этого аккаунта нет сохраненного WebDAV-пароля. Попросите администратора сбросить пароль в панели.",
        "file_unknown": "Не удалось определить файл для загрузки.",
        "file_too_big": "Telegram не дает боту скачать этот файл: он больше <b>{limit} MB</b>.\n\nЗагрузите большой файл напрямую через веб-интерфейс облака.",
        "upload_queued": "📥 <b>{filename}</b> ({size}) добавлен в очередь.\n\nМесто в очереди: <b>{position}</b>.",
        "upload_queued_supporter": "⭐ У вас премиум-приоритет: загрузка пройдет раньше обычной очереди.",
        "uploading": "Загружаю <b>{filename}</b> ({size}) в облако...",
        "uploaded": "Файл загружен",
        "path": "Путь",
        "telegram_download_failed": "Telegram не дает боту скачать этот файл.\n\nЛимит для загрузки через бота: <b>{limit} MB</b>.\nЗагрузите большой файл напрямую через облако.",
        "upload_failed": "Не удалось загрузить файл в облако",
        "processing_failed": "Не удалось обработать файл",
        "approved_title": "Ваша заявка одобрена",
        "approved_hint": "Файлы можно отправлять прямо сюда: бот загрузит их в облако.\nПароль всегда виден в /start, там же его можно сменить.",
        "request_sent_title": "Заявка отправлена ✨",
        "request_sent": "Администратор проверит доступ к beta-тесту. Я сообщу, когда аккаунт будет готов.",
        "request_rejected": "Ваша заявка на beta-тест сейчас отклонена.",
        "account_missing": "Аккаунт не найден в облаке, запись бота очищена. Отправьте /start еще раз.",
        "used": "Занято",
        "available": "Доступно",
        "free": "свободно",
        "unknown": "неизвестно",
        "usage_failed": "не удалось обновить",
    },
    "en": {
        "account_title": "Your Cloud",
        "server": "Server",
        "login": "Login",
        "password": "Password",
        "password_missing": "not saved",
        "quota": "Quota",
        "upload_hint": "Send a file to this chat and the bot will upload it to the cloud.",
        "support": "Support",
        "support_title": "Support",
        "support_empty": "Support contacts are not configured yet.",
        "support_disabled": "Support section is disabled.",
        "donate_title": "Support the project",
        "donate_empty": "Donation methods are not configured yet.",
        "donate_text": "You can support the project with Telegram Stars or Platega.",
        "donate_disabled": "Donation section is disabled.",
        "donate_choose": "Choose a support method.",
        "stars_title": "Telegram Stars",
        "stars_text": "Choose an amount in Stars.",
        "stars_invoice_title": "Project support",
        "stars_invoice_description": "Thank you for supporting the project!",
        "stars_paid": "Thank you for the support! Premium icon is now active.",
        "platega_title": "Platega",
        "platega_text": "Choose an amount in RUB. The bot will create a Platega payment link.",
        "platega_disabled": "Platega is disabled.",
        "platega_not_configured": "Platega is not configured: set PLATEGA_MERCHANT_ID and PLATEGA_SECRET or provide PLATEGA_URL.",
        "platega_created": "Payment created. Press Check payment after paying.",
        "platega_pending": "Payment is not confirmed yet. Status: {status}",
        "platega_paid": "Payment confirmed! Premium icon is now active.",
        "platega_failed": "Payment is not active. Status: {status}",
        "platega_create_failed": "Could not create Platega payment",
        "platega_check_failed": "Could not check Platega payment",
        "premium_badge": "Premium support",
        "language_title": "Choose language",
        "language_saved": "Language saved.",
        "change_password_prompt": "Send a new cloud password.\n\nMinimum 8 characters. The bot will update the saved password for uploads.",
        "password_invalid": "Password must be 8 to 128 characters.",
        "password_changed": "Password changed.",
        "password_change_failed": "Could not change password",
        "access_inactive": "Access is not active.",
        "upload_not_allowed": "Uploads are available only to approved active users.",
        "webdav_password_missing": "No saved WebDAV password for this account. Ask an admin to reset the password.",
        "file_unknown": "Could not detect a file to upload.",
        "file_too_big": "Telegram does not allow the bot to download this file: it is larger than <b>{limit} MB</b>.\n\nUpload large files directly through the cloud web interface.",
        "upload_queued": "📥 <b>{filename}</b> ({size}) has been added to the queue.\n\nQueue position: <b>{position}</b>.",
        "upload_queued_supporter": "⭐ You have premium priority: your upload will go before the regular queue.",
        "uploading": "Uploading <b>{filename}</b> ({size}) to the cloud...",
        "uploaded": "File uploaded",
        "path": "Path",
        "telegram_download_failed": "Telegram does not allow the bot to download this file.\n\nBot upload limit: <b>{limit} MB</b>.\nUpload large files directly through the cloud.",
        "upload_failed": "Could not upload file to the cloud",
        "processing_failed": "Could not process file",
        "approved_title": "Your request was approved",
        "approved_hint": "You can send files here and the bot will upload them to the cloud.\nYour password is always visible in /start, and you can change it there.",
        "request_sent_title": "Request sent",
        "request_sent": "The administrator will review beta access. I will notify you when the account is ready.",
        "request_rejected": "Your beta-test request is currently rejected.",
        "account_missing": "The account was not found in the cloud, so the bot record was cleared. Send /start again.",
        "used": "Used",
        "available": "Available",
        "free": "free",
        "unknown": "unknown",
        "usage_failed": "could not refresh",
    },
}


def lang_of(user: dict | None) -> str:
    language = (user or {}).get("language") or "ru"
    return language if language in TEXT else "ru"


def tr(lang: str, key: str, **kwargs) -> str:
    value = TEXT.get(lang, TEXT["ru"]).get(key, TEXT["ru"].get(key, key))
    return value.format(**kwargs) if kwargs else value


def event_mark(event: str) -> str:
    return {
        "welcome": "☁️",
        "approved": "✅",
        "upload_ok": "📦",
        "error": "⚠️",
        "sync": "🔄",
        "backup": "🗄️",
        "support": "💬",
        "donate": "💙",
        "language": "🌐",
        "password": "🔐",
        "premium": "⭐",
    }.get(event, "•")


def config_sticker(config: Config, event: str) -> str | None:
    return {
        "welcome": config.sticker_welcome,
        "approved": config.sticker_approved,
        "upload_ok": config.sticker_upload_ok,
        "error": config.sticker_error,
    }.get(event)


async def send_event_sticker(bot: Bot, db: Database, config: Config, chat_id: int, event: str) -> None:
    setting_key = f"sticker_{event}"
    custom_sticker_id = await db.get_setting(setting_key)
    sticker_id = custom_sticker_id or config_sticker(config, event)
    if not sticker_id:
        return
    try:
        await bot.send_sticker(chat_id, sticker_id)
    except TelegramBadRequest as exc:
        logging.warning("Sticker %s is invalid or unavailable, falling back to text marker: %s", event, exc)
        if custom_sticker_id:
            await db.delete_setting(setting_key)
    except Exception:
        logging.exception("Failed to send sticker event=%s chat_id=%s", event, chat_id)


async def safe_edit_text(message, text: str, **kwargs) -> None:
    try:
        await message.edit_text(text, **kwargs)
    except TelegramBadRequest as exc:
        if "message is not modified" in str(exc):
            return
        raise


def telegram_download_limit_bytes(config: Config) -> int:
    return config.telegram_max_download_mb * 1024 * 1024


def support_enabled(config: Config) -> bool:
    return config.enable_support_block


def donate_enabled(config: Config) -> bool:
    return config.enable_donate_block


def account_markup(config: Config, lang: str):
    return account_keyboard(lang, show_support=support_enabled(config), show_donate=donate_enabled(config))


def stars_amounts(config: Config) -> tuple[int, ...]:
    if not config.telegram_stars_enabled:
        return ()
    return config.telegram_stars_amounts


def platega_url(config: Config) -> str | None:
    return config.platega_url if config.platega_enabled else None


def platega_api_enabled(config: Config) -> bool:
    return bool(config.platega_enabled and config.platega_merchant_id and config.platega_secret)


def support_text(config: Config, lang: str = "ru") -> str:
    lines = [f"<b>{tr(lang, 'support_title')}</b>", ""]
    if config.support_telegram:
        telegram = config.support_telegram.strip()
        if telegram.startswith("http://") or telegram.startswith("https://"):
            lines.append(f'Telegram: <a href="{html.escape(telegram)}">{html.escape(telegram)}</a>')
        else:
            username = telegram.lstrip("@")
            lines.append(f'Telegram: <a href="https://t.me/{html.escape(username)}">@{html.escape(username)}</a>')
    if config.support_email:
        lines.append(f'Email: <a href="mailto:{html.escape(config.support_email)}">{html.escape(config.support_email)}</a>')
    if len(lines) == 2:
        lines.append(tr(lang, "support_empty"))
    return "\n".join(lines)


def donate_text(config: Config, lang: str = "ru") -> str:
    if not stars_amounts(config) and not platega_url(config) and not platega_api_enabled(config) and not config.donate_url:
        return f"<b>{tr(lang, 'donate_title')}</b>\n\n{tr(lang, 'donate_empty')}"
    lines = [
        f"<b>{tr(lang, 'donate_title')}</b>",
        "",
        tr(lang, "donate_text"),
        tr(lang, "donate_choose"),
    ]
    if stars_amounts(config):
        lines.append("⭐ Telegram Stars")
    if platega_url(config) or platega_api_enabled(config):
        lines.append("💳 Platega")
    if config.donate_url:
        lines.append(f'💙 <a href="{html.escape(config.donate_url)}">{html.escape(config.donate_url)}</a>')
    return "\n".join(lines)


def format_bytes(value: int | None) -> str:
    if value is None:
        return "неизвестно"
    if value < 0:
        return {
            -1: "не рассчитано",
            -2: "неизвестно",
            -3: "без лимита",
        }.get(value, "неизвестно")
    units = ["B", "KB", "MB", "GB", "TB"]
    size = float(value)
    for unit in units:
        if size < 1024 or unit == units[-1]:
            return f"{size:.1f} {unit}" if unit != "B" else f"{int(size)} B"
        size /= 1024
    return f"{value} B"


def usage_bar(used: int | None, available: int | None, width: int = 12) -> str:
    if used is None or available is None:
        return "[" + "-" * width + "]"
    total = used + available
    if total <= 0:
        return "[" + "-" * width + "]"
    filled = min(width, max(0, round((used / total) * width)))
    return "[" + "#" * filled + "-" * (width - filled) + "]"


async def storage_text(user: dict, nc: NextcloudClient, lang: str = "ru") -> str:
    if not user.get("nc_user_id") or not user.get("nc_password"):
        return f"{tr(lang, 'used')}: <b>{tr(lang, 'unknown')}</b>"
    try:
        quota = await nc.get_quota(user["nc_user_id"], user["nc_password"])
    except Exception as exc:
        logging.warning("Failed to fetch quota for %s: %s", user["telegram_id"], exc)
        return f"{tr(lang, 'used')}: <b>{tr(lang, 'usage_failed')}</b>"

    used = quota["used"]
    available = quota["available"]
    total = used + available if used is not None and available is not None and available >= 0 else None
    if used == 0 and available is not None and available >= 0:
        return (
            f"☁️ {tr(lang, 'used')}: <b>0 B</b>\n"
            f"🟢 {tr(lang, 'available')}: <b>{format_bytes(available)}</b>\n"
            f"📊 <code>{usage_bar(used, available)}</code> 0.0%"
        )
    if total:
        percent = used / total * 100 if used is not None else 0
        return (
            f"☁️ {tr(lang, 'used')}: <b>{format_bytes(used)}</b> / <b>{format_bytes(total)}</b>\n"
            f"📊 <code>{usage_bar(used, available)}</code> {percent:.1f}%"
        )
    return f"☁️ {tr(lang, 'used')}: <b>{format_bytes(used)}</b>, 🟢 {tr(lang, 'free')}: <b>{format_bytes(available)}</b>"


async def account_text(user: dict, nc: NextcloudClient, config: Config) -> str:
    lang = lang_of(user)
    supporter_line = f"⭐ <b>{tr(lang, 'premium_badge')}</b> до <b>{premium_until_text(user)}</b>\n" if is_premium(user) else ""
    password = user.get("nc_password")
    password_line = (
        f"🔐 {tr(lang, 'password')}: <code>{html.escape(password)}</code>\n"
        if password
        else f"🔐 {tr(lang, 'password')}: <b>{tr(lang, 'password_missing')}</b>\n"
    )
    return (
        f"{event_mark('welcome')} ✨ <b>{tr(lang, 'account_title')}</b> ✨\n"
        "<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n"
        f"{supporter_line}"
        f"🆔 {tr(lang, 'login')}: <code>{html.escape(user.get('nc_user_id') or str(user['telegram_id']))}</code>\n"
        f"{password_line}"
        f"💾 {tr(lang, 'quota')}: <b>{user['quota_gb']} GB</b>\n"
        "\n"
        f"{await storage_text(user, nc, lang)}\n\n"
        f"📤 {tr(lang, 'upload_hint')}"
    )


def clean_filename(filename: str) -> str:
    filename = Path(filename).name.strip()
    filename = re.sub(r"[^A-Za-z0-9А-Яа-я._ -]+", "_", filename)
    filename = re.sub(r"\s+", " ", filename).strip(" .")
    return filename[:120] or "upload.bin"


def upload_target_from_message(message: Message) -> tuple[str, str, int] | None:
    if message.document:
        return (
            message.document.file_id,
            clean_filename(message.document.file_name or f"document_{message.message_id}.bin"),
            message.document.file_size or 0,
        )
    if message.photo:
        photo = message.photo[-1]
        return (photo.file_id, f"photo_{message.message_id}.jpg", photo.file_size or 0)
    if message.video:
        return (
            message.video.file_id,
            clean_filename(message.video.file_name or f"video_{message.message_id}.mp4"),
            message.video.file_size or 0,
        )
    if message.audio:
        return (
            message.audio.file_id,
            clean_filename(message.audio.file_name or f"audio_{message.message_id}.mp3"),
            message.audio.file_size or 0,
        )
    if message.voice:
        return (message.voice.file_id, f"voice_{message.message_id}.ogg", message.voice.file_size or 0)
    if message.video_note:
        return (message.video_note.file_id, f"video_note_{message.message_id}.mp4", message.video_note.file_size or 0)
    if message.animation:
        return (
            message.animation.file_id,
            clean_filename(message.animation.file_name or f"animation_{message.message_id}.mp4"),
            message.animation.file_size or 0,
        )
    return None


async def admin_summary_text(db: Database) -> str:
    total = await db.count_users()
    requested = await db.count_users("requested")
    approved = await db.count_users("approved")
    rejected = await db.count_users("rejected")
    return (
        "<b>Админ-панель Nextcloud</b>\n"
        "<code>--------------------------------</code>\n\n"
        f"Всего пользователей: <b>{total}</b>\n"
        f"Заявок: <b>{requested}</b>\n"
        f"Одобрено: <b>{approved}</b>\n"
        f"Отклонено: <b>{rejected}</b>"
    )


async def notify_admins(bot: Bot, config: Config, text: str, reply_markup=None) -> None:
    for admin_id in config.admin_ids:
        try:
            await bot.send_message(admin_id, text, reply_markup=reply_markup)
        except Exception:
            logging.exception("Failed to notify admin %s", admin_id)


async def sync_nextcloud_users(db: Database, nc: NextcloudClient) -> tuple[int, int]:
    checked = 0
    removed = 0
    for user in await db.approved_users():
        nc_user_id = user.get("nc_user_id")
        if not nc_user_id:
            continue
        checked += 1
        if not await nc.user_exists(nc_user_id):
            logging.warning(
                "Sync removed Telegram user %s because Nextcloud user %s is missing",
                user["telegram_id"],
                nc_user_id,
            )
            await db.delete_user(int(user["telegram_id"]))
            removed += 1
    logging.info("Nextcloud sync completed: checked=%s removed=%s", checked, removed)
    return checked, removed


@router.message(CommandStart())
async def start(message: Message, bot: Bot, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not message.from_user:
        return

    telegram_id = message.from_user.id
    if is_admin(telegram_id, config):
        await message.answer(await admin_summary_text(db), reply_markup=admin_keyboard())
        return

    user = await db.upsert_request(
        telegram_id=telegram_id,
        username=message.from_user.username,
        first_name=message.from_user.first_name,
        last_name=message.from_user.last_name,
    )

    if user["status"] == "approved":
        if user.get("nc_user_id") and not await nc.user_exists(user["nc_user_id"]):
            logging.warning("Nextcloud user %s is missing, deleting bot DB record", user["nc_user_id"])
            await db.delete_user(telegram_id)
            await message.answer("Аккаунт не найден в Nextcloud, запись бота очищена. Отправьте /start еще раз.")
            return
        await send_event_sticker(bot, db, config, message.chat.id, "welcome")
        await message.answer(await account_text(user, nc, config), reply_markup=account_markup(config, lang_of(user)))
        logging.info("Approved user opened account panel: telegram_id=%s", telegram_id)
        return

    if user["status"] == "rejected":
        await message.answer(tr(lang_of(user), "request_rejected"))
        return

    lang = lang_of(user)
    await message.answer(f"<b>{tr(lang, 'request_sent_title')}</b>\n\n{tr(lang, 'request_sent')}")
    logging.info("Beta request created/updated: telegram_id=%s username=%s", telegram_id, message.from_user.username)
    admin_text = (
        "<b>Новая заявка на beta-тест</b>\n"
        "<code>--------------------------------</code>\n\n"
        f"Пользователь: {display_name(user)}\n"
        f"Telegram ID: <code>{telegram_id}</code>"
    )
    await notify_admins(bot, config, admin_text, request_review_keyboard(telegram_id))


@router.message(Command("admin"))
async def admin_command(message: Message, db: Database, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config):
        return
    await message.answer(await admin_summary_text(db), reply_markup=admin_keyboard())


@router.message(Command("health"))
async def health_command(message: Message, nc: NextcloudClient, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config):
        return
    internal_note = ""
    if config.nextcloud_internal_url == config.nextcloud_url:
        internal_note = (
            "\n\nВнутренний URL совпадает с публичным. Если бот и Nextcloud на одном сервере, "
            "обычно лучше задать <code>NEXTCLOUD_INTERNAL_URL</code>."
        )
    try:
        await nc.check_connection()
        status = "Nextcloud API доступен"
    except Exception as exc:
        status = f"Nextcloud API недоступен: <code>{html.escape(str(exc))}</code>"
    await message.answer(
        "<b>Проверка Nextcloud</b>\n\n"
        f"Публичный URL: <code>{html.escape(config.nextcloud_url)}</code>\n"
        f"Внутренний URL: <code>{html.escape(config.nextcloud_internal_url)}</code>\n\n"
        f"{status}{internal_note}"
    )


@router.message(Command("sync"))
async def sync_command(message: Message, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config):
        return
    try:
        checked, removed = await sync_nextcloud_users(db, nc)
    except Exception as exc:
        logging.exception("Manual Nextcloud sync failed")
        await message.answer(f"{event_mark('error')} Синхронизация не удалась: <code>{html.escape(str(exc))}</code>")
        return
    await message.answer(f"{event_mark('sync')} Синхронизация завершена.\nПроверено: <b>{checked}</b>\nУдалено из БД бота: <b>{removed}</b>")


async def stickers_text(db: Database, config: Config) -> str:
    settings = await db.list_settings("sticker_")
    return (
        "<b>Стикеры</b>\n\n"
        "Если кастомный стикер не задан или Telegram его отклонит, бот оставит базовый маркер в тексте.\n\n"
        f"welcome: <b>{'кастомный' if settings.get('sticker_welcome') or config.sticker_welcome else 'базовый'}</b> {event_mark('welcome')}\n"
        f"approved: <b>{'кастомный' if settings.get('sticker_approved') or config.sticker_approved else 'базовый'}</b> {event_mark('approved')}\n"
        f"upload_ok: <b>{'кастомный' if settings.get('sticker_upload_ok') or config.sticker_upload_ok else 'базовый'}</b> {event_mark('upload_ok')}\n"
        f"error: <b>{'кастомный' if settings.get('sticker_error') or config.sticker_error else 'базовый'}</b> {event_mark('error')}\n"
        f"support: <b>{'кастомный' if settings.get('sticker_support') else 'базовый'}</b> {event_mark('support')}\n"
        f"donate: <b>{'кастомный' if settings.get('sticker_donate') else 'базовый'}</b> {event_mark('donate')}\n"
        f"language: <b>{'кастомный' if settings.get('sticker_language') else 'базовый'}</b> {event_mark('language')}\n"
        f"password: <b>{'кастомный' if settings.get('sticker_password') else 'базовый'}</b> {event_mark('password')}\n\n"
        "Команды настройки:\n"
        "<code>/setsticker welcome</code>\n"
        "<code>/setsticker approved</code>\n"
        "<code>/setsticker upload_ok</code>\n"
        "<code>/setsticker error</code>\n"
        "<code>/setsticker support</code>\n"
        "<code>/setsticker donate</code>\n"
        "<code>/setsticker language</code>\n"
        "<code>/setsticker password</code>\n\n"
        "После команды отправьте нужный стикер."
    )


@router.message(Command("stickers"))
async def stickers_command(message: Message, db: Database, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config):
        return
    await message.answer(await stickers_text(db, config))


@router.callback_query(F.data == "stickers")
async def stickers_panel(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not callback.from_user or not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    await safe_edit_text(callback.message, await stickers_text(db, config), reply_markup=admin_keyboard())
    await callback.answer()


@router.message(Command("setsticker"))
async def set_sticker_command(message: Message, state: FSMContext, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config):
        return
    parts = (message.text or "").split(maxsplit=1)
    allowed = {"welcome", "approved", "upload_ok", "error", "support", "donate", "language", "password"}
    if len(parts) != 2 or parts[1].strip() not in allowed:
        await message.answer("Используйте: <code>/setsticker welcome|approved|upload_ok|error|support|donate|language|password</code>")
        return
    event = parts[1].strip()
    await state.set_state(StickerState.waiting_sticker)
    await state.update_data(sticker_event=event)
    await message.answer(f"Отправьте стикер для события <code>{event}</code>.")


@router.message(StickerState.waiting_sticker, F.sticker)
async def save_sticker(message: Message, state: FSMContext, db: Database, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config) or not message.sticker:
        return
    data = await state.get_data()
    event = data["sticker_event"]
    await db.set_setting(f"sticker_{event}", message.sticker.file_id)
    await state.clear()
    await message.answer(f"Кастомный стикер для <code>{event}</code> сохранен. Если Telegram его не примет, останется базовый маркер {event_mark(event)}.")


@router.callback_query(F.data == "account:support")
async def account_support(callback: CallbackQuery, bot: Bot, db: Database, config: Config) -> None:
    if not support_enabled(config):
        await callback.answer(tr("ru", "support_disabled"), show_alert=True)
        return
    user = await db.get_user(callback.from_user.id)
    lang = lang_of(user)
    await send_event_sticker(bot, db, config, callback.message.chat.id, "support")
    await safe_edit_text(
        callback.message,
        support_text(config, lang),
        reply_markup=support_keyboard(lang),
    )
    await callback.answer()


@router.callback_query(F.data == "account:donate")
async def account_donate(callback: CallbackQuery, bot: Bot, db: Database, config: Config) -> None:
    if not donate_enabled(config):
        await callback.answer(tr("ru", "donate_disabled"), show_alert=True)
        return
    user = await db.get_user(callback.from_user.id)
    lang = lang_of(user)
    await send_event_sticker(bot, db, config, callback.message.chat.id, "donate")
    await safe_edit_text(
        callback.message,
        donate_text(config, lang),
        reply_markup=donate_keyboard(
            lang,
            show_stars=bool(stars_amounts(config)),
            show_platega=bool(platega_url(config) or platega_api_enabled(config)),
            donate_url=config.donate_url,
        ),
    )
    await callback.answer()


@router.callback_query(F.data == "donate:stars")
async def donate_stars_panel(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not donate_enabled(config) or not stars_amounts(config):
        await callback.answer(tr("ru", "donate_disabled"), show_alert=True)
        return
    user = await db.get_user(callback.from_user.id)
    lang = lang_of(user)
    await safe_edit_text(
        callback.message,
        f"⭐ <b>{tr(lang, 'stars_title')}</b>\n\n{tr(lang, 'stars_text')}",
        reply_markup=stars_amounts_keyboard(lang, stars_amounts(config)),
    )
    await callback.answer()


@router.callback_query(F.data.startswith("stars:"))
async def stars_donate(callback: CallbackQuery, db: Database, bot: Bot, config: Config) -> None:
    if not donate_enabled(config) or not config.telegram_stars_enabled:
        await callback.answer(tr("ru", "donate_disabled"), show_alert=True)
        return
    user = await db.get_user(callback.from_user.id)
    lang = lang_of(user)
    if not user or user["status"] != "approved" or user["is_disabled"]:
        await callback.answer(tr(lang, "access_inactive"), show_alert=True)
        return
    amount = int(callback.data.split(":", 1)[1])
    if amount not in stars_amounts(config):
        await callback.answer("Недоступная сумма", show_alert=True)
        return
    await bot.send_invoice(
        chat_id=callback.message.chat.id,
        title=tr(lang, "stars_invoice_title"),
        description=tr(lang, "stars_invoice_description"),
        payload=f"stars_donate:{callback.from_user.id}:{amount}",
        provider_token="",
        currency="XTR",
        prices=[LabeledPrice(label=f"{amount} Stars", amount=amount)],
    )
    await callback.answer()


@router.callback_query(F.data == "donate:platega")
async def donate_platega_panel(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not donate_enabled(config) or not config.platega_enabled:
        await callback.answer(tr("ru", "platega_disabled"), show_alert=True)
        return
    user = await db.get_user(callback.from_user.id)
    lang = lang_of(user)
    if not platega_api_enabled(config) and not platega_url(config):
        await callback.answer(tr(lang, "platega_not_configured"), show_alert=True)
        return
    await safe_edit_text(
        callback.message,
        f"💳 <b>{tr(lang, 'platega_title')}</b>\n\n{tr(lang, 'platega_text')}",
        reply_markup=platega_amounts_keyboard(
            lang,
            config.platega_amounts_rub if platega_api_enabled(config) else (),
            platega_url(config),
        ),
    )
    await callback.answer()


@router.callback_query(F.data.startswith("platega:"))
async def platega_create_payment(
    callback: CallbackQuery,
    db: Database,
    platega: PlategaClient | None,
    config: Config,
) -> None:
    if not donate_enabled(config) or not config.platega_enabled:
        await callback.answer(tr("ru", "platega_disabled"), show_alert=True)
        return
    user = await db.get_user(callback.from_user.id)
    lang = lang_of(user)
    if not user or user["status"] != "approved" or user["is_disabled"]:
        await callback.answer(tr(lang, "access_inactive"), show_alert=True)
        return
    if not platega:
        await callback.answer(tr(lang, "platega_not_configured"), show_alert=True)
        return
    amount = int(callback.data.split(":", 1)[1])
    if amount not in config.platega_amounts_rub:
        await callback.answer("Недоступная сумма", show_alert=True)
        return

    payload = f"platega_donate:{callback.from_user.id}:{amount}"
    try:
        payment = await platega.create_payment_link(
            amount_rub=amount,
            description=f"Cloud bot support from Telegram {callback.from_user.id}",
            payload=payload,
            return_url=config.platega_return_url,
            failed_url=config.platega_failed_url,
        )
    except PlategaError as exc:
        logging.exception("Failed to create Platega payment")
        await callback.answer(tr(lang, "platega_create_failed"), show_alert=True)
        await callback.message.answer(f"{event_mark('error')} {tr(lang, 'platega_create_failed')}: <code>{html.escape(str(exc))}</code>")
        return

    transaction_id = str(payment["transactionId"])
    payment_url = str(payment["url"])
    status = str(payment.get("status") or "PENDING")
    await db.create_payment(
        transaction_id=transaction_id,
        telegram_id=callback.from_user.id,
        provider="platega",
        amount=amount,
        currency="RUB",
        status=status,
        payment_url=payment_url,
        payload=payload,
    )
    logging.info("Platega payment created: telegram_id=%s transaction_id=%s amount=%s", callback.from_user.id, transaction_id, amount)
    await safe_edit_text(
        callback.message,
        f"💳 <b>{tr(lang, 'platega_title')}</b>\n\n"
        f"{tr(lang, 'platega_created')}\n"
        f"ID: <code>{html.escape(transaction_id)}</code>\n"
        f"Сумма: <b>{amount} RUB</b>",
        reply_markup=platega_payment_keyboard(lang, payment_url, transaction_id),
    )
    await callback.answer()


@router.callback_query(F.data.startswith("platega_check:"))
async def platega_check_payment(
    callback: CallbackQuery,
    db: Database,
    platega: PlategaClient | None,
    config: Config,
) -> None:
    transaction_id = callback.data.split(":", 1)[1]
    payment = await db.get_payment(transaction_id)
    if not payment:
        await callback.answer("Платеж не найден", show_alert=True)
        return
    if payment["telegram_id"] != callback.from_user.id and not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    user = await db.get_user(int(payment["telegram_id"]))
    lang = lang_of(user)
    if not platega:
        await callback.answer(tr(lang, "platega_not_configured"), show_alert=True)
        return
    try:
        status_payload = await platega.get_transaction(transaction_id)
    except PlategaError as exc:
        logging.exception("Failed to check Platega payment")
        await callback.answer(tr(lang, "platega_check_failed"), show_alert=True)
        await callback.message.answer(f"{event_mark('error')} {tr(lang, 'platega_check_failed')}: <code>{html.escape(str(exc))}</code>")
        return

    status = str(status_payload.get("status") or "").upper()
    await db.update_payment_status(transaction_id, status)
    if status == "CONFIRMED":
        await db.set_supporter(int(payment["telegram_id"]), True, premium_until(config))
        logging.info("Platega payment confirmed: telegram_id=%s transaction_id=%s", payment["telegram_id"], transaction_id)
        await callback.message.answer(
            f"{event_mark('premium')} {tr(lang, 'platega_paid')}",
            reply_markup=account_markup(config, lang),
        )
        await callback.answer("Оплачено")
        return
    if status in {"CANCELED", "CHARGEBACKED", "FAILED", "EXPIRED"}:
        await callback.answer(tr(lang, "platega_failed", status=status), show_alert=True)
        return
    await callback.answer(tr(lang, "platega_pending", status=status or "UNKNOWN"), show_alert=True)


@router.pre_checkout_query()
async def stars_pre_checkout(pre_checkout: PreCheckoutQuery) -> None:
    await pre_checkout.answer(ok=pre_checkout.invoice_payload.startswith("stars_donate:"))


@router.message(F.successful_payment)
async def stars_payment_success(message: Message, db: Database, config: Config) -> None:
    if not message.from_user or not message.successful_payment:
        return
    if not message.successful_payment.invoice_payload.startswith("stars_donate:"):
        return
    user = await db.get_user(message.from_user.id)
    lang = lang_of(user)
    await db.set_supporter(message.from_user.id, True, premium_until(config))
    logging.info(
        "Telegram Stars donation received: telegram_id=%s charge_id=%s stars=%s",
        message.from_user.id,
        message.successful_payment.telegram_payment_charge_id,
        message.successful_payment.total_amount,
    )
    await message.answer(f"{event_mark('premium')} {tr(lang, 'stars_paid')}", reply_markup=account_markup(config, lang))


@router.callback_query(F.data == "account:language")
async def account_language(callback: CallbackQuery, bot: Bot, db: Database, config: Config) -> None:
    user = await db.get_user(callback.from_user.id)
    lang = lang_of(user)
    await send_event_sticker(bot, db, config, callback.message.chat.id, "language")
    await safe_edit_text(
        callback.message,
        f"<b>{tr(lang, 'language_title')}</b>",
        reply_markup=language_keyboard(lang),
    )
    await callback.answer()


@router.callback_query(F.data.startswith("lang:"))
async def account_set_language(callback: CallbackQuery, db: Database, nc: NextcloudClient, config: Config) -> None:
    language = callback.data.split(":", 1)[1]
    if language not in {"ru", "en"}:
        await callback.answer("Invalid language", show_alert=True)
        return
    user = await db.get_user(callback.from_user.id)
    if not user:
        await callback.answer("No account", show_alert=True)
        return
    await db.set_language(callback.from_user.id, language)
    user = await db.get_user(callback.from_user.id)
    await safe_edit_text(callback.message, await account_text(user, nc, config), reply_markup=account_markup(config, language))
    await callback.answer(tr(language, "language_saved"))


@router.callback_query(F.data == "account:home")
async def account_home(callback: CallbackQuery, state: FSMContext, db: Database, nc: NextcloudClient, config: Config) -> None:
    await state.clear()
    user = await db.get_user(callback.from_user.id)
    if not user or user["status"] != "approved" or user["is_disabled"]:
        await callback.answer(tr(lang_of(user), "access_inactive"), show_alert=True)
        return
    lang = lang_of(user)
    await safe_edit_text(callback.message, await account_text(user, nc, config), reply_markup=account_markup(config, lang))
    await callback.answer()


@router.callback_query(F.data == "sync")
async def sync_panel(callback: CallbackQuery, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not callback.from_user or not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    try:
        checked, removed = await sync_nextcloud_users(db, nc)
    except Exception as exc:
        logging.exception("Manual Nextcloud sync failed")
        await callback.message.answer(f"{event_mark('error')} Синхронизация не удалась: <code>{html.escape(str(exc))}</code>")
        await callback.answer("Ошибка", show_alert=True)
        return
    await safe_edit_text(
        callback.message,
        f"{event_mark('sync')} <b>Синхронизация завершена</b>\n\n"
        f"Проверено: <b>{checked}</b>\n"
        f"Удалено из БД бота: <b>{removed}</b>",
        reply_markup=admin_keyboard(),
    )
    await callback.answer("Готово")


@router.callback_query(F.data == "account:change_password")
async def account_change_password(callback: CallbackQuery, state: FSMContext, db: Database, config: Config) -> None:
    user = await db.get_user(callback.from_user.id)
    lang = lang_of(user)
    if not user or user["status"] != "approved" or user["is_disabled"]:
        await callback.answer(tr(lang, "access_inactive"), show_alert=True)
        return
    await state.set_state(UserPasswordState.waiting_password)
    await callback.message.answer(
        tr(lang, "change_password_prompt"),
        reply_markup=account_back_keyboard(lang),
    )
    await callback.answer()


@router.message(UserPasswordState.waiting_password)
async def account_change_password_apply(
    message: Message,
    bot: Bot,
    state: FSMContext,
    db: Database,
    nc: NextcloudClient,
    config: Config,
) -> None:
    if not message.from_user:
        return
    user = await db.get_user(message.from_user.id)
    lang = lang_of(user)
    if not user or user["status"] != "approved" or user["is_disabled"] or not user.get("nc_user_id"):
        await state.clear()
        await message.answer(tr(lang, "access_inactive"))
        return

    password = (message.text or "").strip()
    if not valid_password(password):
        await message.answer(tr(lang, "password_invalid"), reply_markup=account_back_keyboard(lang))
        return

    try:
        await nc.set_user_value(user["nc_user_id"], "password", password)
    except NextcloudError as exc:
        await message.answer(f"{event_mark('error')} {tr(lang, 'password_change_failed')}: <code>{html.escape(str(exc))}</code>")
        return

    await db.set_nextcloud_password(user["telegram_id"], password)
    await state.clear()
    await send_event_sticker(bot, db, config, message.chat.id, "password")
    await message.answer(
        f"{event_mark('approved')} {tr(lang, 'password_changed')}\n\n"
        f"{tr(lang, 'login')}: <code>{html.escape(user['nc_user_id'])}</code>\n"
        f"{tr(lang, 'password')}: <code>{html.escape(password)}</code>",
        reply_markup=account_markup(config, lang),
    )


async def process_upload_job(
    job: UploadJob,
    bot: Bot,
    db: Database,
    nc: NextcloudClient,
    config: Config,
) -> None:
    user = await db.get_user(job.telegram_id)
    lang = lang_of(user) if user else job.lang
    if not user or user["status"] != "approved" or user["is_disabled"]:
        await bot.edit_message_text(
            tr(lang, "upload_not_allowed"),
            chat_id=job.chat_id,
            message_id=job.status_message_id,
        )
        return
    if not user.get("nc_user_id") or not user.get("nc_password"):
        await bot.edit_message_text(
            tr(lang, "webdav_password_missing"),
            chat_id=job.chat_id,
            message_id=job.status_message_id,
        )
        return

    await bot.edit_message_text(
        tr(lang, "uploading", filename=html.escape(job.filename), size=format_bytes(job.file_size)),
        chat_id=job.chat_id,
        message_id=job.status_message_id,
    )

    temp_file = tempfile.NamedTemporaryFile(prefix="tg-nextcloud-", delete=False)
    temp_path = Path(temp_file.name)
    temp_file.close()
    try:
        await bot.download(job.file_id, destination=temp_path)
        remote_path = await nc.upload_file(user["nc_user_id"], user["nc_password"], "", job.filename, temp_path)
        logging.info(
            "Upload completed from queue: telegram_id=%s remote_path=%s size=%s premium_priority=%s",
            user["telegram_id"],
            remote_path,
            job.file_size,
            job.is_supporter,
        )
        await send_event_sticker(bot, db, config, job.chat_id, "upload_ok")
        await bot.edit_message_text(
            f"{event_mark('upload_ok')} <b>{tr(lang, 'uploaded')}</b>\n\n"
            f"{tr(lang, 'path')}: <code>{html.escape(remote_path)}</code>\n\n"
            f"{await storage_text(user, nc, lang)}",
            chat_id=job.chat_id,
            message_id=job.status_message_id,
        )
    except NextcloudError as exc:
        logging.warning("Queued upload failed for telegram_id=%s filename=%s: %s", job.telegram_id, job.filename, exc)
        await send_event_sticker(bot, db, config, job.chat_id, "error")
        await bot.edit_message_text(
            f"{event_mark('error')} {tr(lang, 'upload_failed')}: <code>{html.escape(str(exc))}</code>",
            chat_id=job.chat_id,
            message_id=job.status_message_id,
        )
    except TelegramBadRequest as exc:
        logging.warning(
            "Telegram refused queued file download: telegram_id=%s filename=%s size=%s: %s",
            job.telegram_id,
            job.filename,
            job.file_size,
            exc,
        )
        await bot.edit_message_text(
            f"{event_mark('error')} {tr(lang, 'telegram_download_failed', limit=config.telegram_max_download_mb)}",
            chat_id=job.chat_id,
            message_id=job.status_message_id,
        )
    except Exception as exc:
        logging.exception("Failed to process queued Telegram file upload")
        await bot.edit_message_text(
            f"{tr(lang, 'processing_failed')}: <code>{html.escape(str(exc))}</code>",
            chat_id=job.chat_id,
            message_id=job.status_message_id,
        )
    finally:
        temp_path.unlink(missing_ok=True)


@router.message(StateFilter(None), F.document | F.photo | F.video | F.audio | F.voice | F.video_note | F.animation)
async def upload_to_nextcloud(
    message: Message,
    bot: Bot,
    db: Database,
    upload_queue: asyncio.PriorityQueue,
    config: Config,
) -> None:
    if not message.from_user:
        return

    user = await db.get_user(message.from_user.id)
    lang = lang_of(user)
    if not user or user["status"] != "approved" or user["is_disabled"]:
        await message.answer(tr(lang, "upload_not_allowed"))
        return
    if not user.get("nc_user_id") or not user.get("nc_password"):
        await message.answer(tr(lang, "webdav_password_missing"))
        return

    target = upload_target_from_message(message)
    if not target:
        await message.answer(tr(lang, "file_unknown"))
        return
    file_id, filename, file_size = target
    max_download = telegram_download_limit_bytes(config)
    if file_size and file_size > max_download:
        logging.info(
            "Telegram file rejected before download: telegram_id=%s filename=%s size=%s limit=%s",
            user["telegram_id"],
            filename,
            file_size,
            max_download,
        )
        await send_event_sticker(bot, db, config, message.chat.id, "error")
        await message.answer(
            f"{event_mark('error')} {tr(lang, 'file_too_big', limit=config.telegram_max_download_mb)}"
        )
        return

    is_supporter = is_premium(user)
    priority = 0 if is_supporter else 10
    position = upload_queue.qsize() + 1
    queue_text = tr(lang, "upload_queued", filename=html.escape(filename), size=format_bytes(file_size), position=position)
    if is_supporter:
        queue_text += f"\n\n{tr(lang, 'upload_queued_supporter')}"
    status_message = await message.answer(queue_text)
    job = UploadJob(
        telegram_id=int(user["telegram_id"]),
        chat_id=message.chat.id,
        status_message_id=status_message.message_id,
        file_id=file_id,
        filename=filename,
        file_size=file_size,
        lang=lang,
        is_supporter=is_supporter,
    )
    await upload_queue.put((priority, next(UPLOAD_QUEUE_COUNTER), job))
    logging.info(
        "Upload queued: telegram_id=%s filename=%s size=%s priority=%s queue_size=%s",
        user["telegram_id"],
        filename,
        file_size,
        priority,
        upload_queue.qsize(),
    )


@router.callback_query(F.data == "admin")
async def admin_panel(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not callback.from_user or not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    await safe_edit_text(callback.message, await admin_summary_text(db), reply_markup=admin_keyboard())
    await callback.answer()


@router.callback_query(F.data.startswith("approve:"))
async def approve_user(callback: CallbackQuery, bot: Bot, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return

    telegram_id = int(callback.data.split(":", 1)[1])
    user = await db.get_user(telegram_id)
    if not user:
        await callback.answer("Пользователь не найден", show_alert=True)
        return

    nc_user_id = str(telegram_id)
    password = generate_password()
    try:
        await nc.ensure_user(nc_user_id, password, config.default_quota_gb)
    except NextcloudError as exc:
        await callback.answer("Ошибка Nextcloud", show_alert=True)
        await callback.message.answer(f"Не удалось выдать доступ: <code>{html.escape(str(exc))}</code>")
        return

    await db.approve_user(telegram_id, nc_user_id, password, config.default_quota_gb)
    user = await db.get_user(telegram_id)
    lang = lang_of(user)
    logging.info("User approved: telegram_id=%s nc_user_id=%s quota_gb=%s", telegram_id, nc_user_id, config.default_quota_gb)
    await send_event_sticker(bot, db, config, telegram_id, "approved")
    await bot.send_message(
        telegram_id,
        f"{event_mark('approved')} <b>{tr(lang, 'approved_title')}</b>\n"
        "<code>--------------------------------</code>\n\n"
        f"🆔 {tr(lang, 'login')}: <code>{nc_user_id}</code>\n"
        f"🔐 {tr(lang, 'password')}: <code>{html.escape(password)}</code>\n"
        f"💾 {tr(lang, 'quota')}: <b>{config.default_quota_gb} GB</b>\n\n"
        f"{tr(lang, 'approved_hint')}",
        reply_markup=account_markup(config, lang),
    )
    await safe_edit_text(
        callback.message,
        f"Доступ выдан пользователю <code>{telegram_id}</code>: {config.default_quota_gb} GB."
    )
    await callback.answer("Одобрено")


@router.callback_query(F.data.startswith("reject:"))
async def reject_user(callback: CallbackQuery, bot: Bot, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    telegram_id = int(callback.data.split(":", 1)[1])
    user = await db.get_user(telegram_id)
    lang = lang_of(user)
    await db.reject_user(telegram_id)
    logging.info("User rejected: telegram_id=%s", telegram_id)
    try:
        await bot.send_message(telegram_id, tr(lang, "request_rejected"))
    except Exception:
        logging.exception("Failed to notify rejected user %s", telegram_id)
    await safe_edit_text(callback.message, f"Заявка пользователя <code>{telegram_id}</code> отклонена.")
    await callback.answer("Отклонено")


@router.callback_query(F.data.startswith("users:"))
async def users_list(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    _, status, page_raw = callback.data.split(":")
    page = int(page_raw)
    query_status = None if status == "all" else status
    users = await db.list_users(query_status, limit=PAGE_SIZE + 1, offset=page * PAGE_SIZE)
    has_next = len(users) > PAGE_SIZE
    users = users[:PAGE_SIZE]
    title = "Все пользователи" if status == "all" else f"Пользователи: {status}"
    text = f"<b>{title}</b>\n\n"
    text += "Выберите пользователя." if users else "Пока пусто."
    await safe_edit_text(callback.message, text, reply_markup=users_keyboard(users, status, page, has_next))
    await callback.answer()


@router.callback_query(F.data.startswith("user:"))
async def user_details(callback: CallbackQuery, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    _, telegram_id_raw, back_status, back_page_raw = callback.data.split(":")
    telegram_id = int(telegram_id_raw)
    await render_user_details(callback, db, nc, config, telegram_id, back_status, int(back_page_raw))
    await callback.answer()


async def render_user_details(
    callback: CallbackQuery,
    db: Database,
    nc: NextcloudClient | None,
    config: Config,
    telegram_id: int,
    back_status: str = "all",
    back_page: int = 0,
) -> None:
    user = await db.get_user(telegram_id)
    if not user:
        await callback.answer("Пользователь не найден", show_alert=True)
        return

    disabled = bool(user["is_disabled"])
    is_supporter = is_premium(user)
    storage = await storage_text(user, nc) if nc and user["status"] == "approved" else "Занято: <b>нет данных</b>"
    text = (
        "<b>Пользователь</b>\n\n"
        f"Имя: {display_name(user)}\n"
        f"Telegram ID: <code>{telegram_id}</code>\n"
        f"Nextcloud ID: <code>{html.escape(user.get('nc_user_id') or '-')}</code>\n"
        f"Статус: <b>{html.escape(user['status'])}</b>\n"
        f"Премиум: <b>{'⭐ до ' + premium_until_text(user) if is_supporter else 'нет'}</b>\n"
        f"Квота: <b>{user['quota_gb']} GB</b>\n"
        f"{storage}\n"
        f"Доступ: <b>{'отключен' if disabled else 'активен'}</b>"
    )
    await safe_edit_text(
        callback.message,
        text,
        reply_markup=user_keyboard(telegram_id, back_status, back_page, user["status"], disabled, is_supporter),
    )


@router.callback_query(F.data.startswith("quotaadd:"))
async def quota_add(callback: CallbackQuery, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    _, telegram_id_raw, amount_raw = callback.data.split(":")
    telegram_id = int(telegram_id_raw)
    amount = int(amount_raw)
    user = await db.get_user(telegram_id)
    if not user or not user.get("nc_user_id"):
        await callback.answer("Nextcloud-пользователь еще не создан", show_alert=True)
        return

    new_quota = int(user["quota_gb"]) + amount
    try:
        await nc.set_quota(user["nc_user_id"], new_quota)
    except NextcloudError as exc:
        await callback.answer("Ошибка Nextcloud", show_alert=True)
        await callback.message.answer(f"Не удалось изменить квоту: <code>{html.escape(str(exc))}</code>")
        return
    await db.set_quota(telegram_id, new_quota)
    await callback.answer(f"Добавлено {amount}GB")
    await render_user_details(callback, db, nc, config, telegram_id)


@router.callback_query(F.data.startswith("quotacustom:"))
async def quota_custom(callback: CallbackQuery, state: FSMContext, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    telegram_id = int(callback.data.split(":", 1)[1])
    user = await db.get_user(telegram_id)
    if not user or not user.get("nc_user_id"):
        await callback.answer("Nextcloud-пользователь еще не создан", show_alert=True)
        return
    await state.set_state(QuotaState.waiting_amount)
    await state.update_data(target_telegram_id=telegram_id)
    await callback.message.answer("Введите, сколько GB добавить пользователю.")
    await callback.answer()


@router.message(QuotaState.waiting_amount)
async def quota_custom_amount(message: Message, state: FSMContext, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config):
        return
    try:
        amount = int((message.text or "").strip())
    except ValueError:
        await message.answer("Введите целое число GB.")
        return
    if amount <= 0:
        await message.answer("Количество GB должно быть больше нуля.")
        return

    data = await state.get_data()
    telegram_id = int(data["target_telegram_id"])
    user = await db.get_user(telegram_id)
    if not user or not user.get("nc_user_id"):
        await message.answer("Пользователь не найден или еще не одобрен.")
        await state.clear()
        return
    new_quota = int(user["quota_gb"]) + amount
    try:
        await nc.set_quota(user["nc_user_id"], new_quota)
    except NextcloudError as exc:
        await message.answer(f"Не удалось изменить квоту: <code>{html.escape(str(exc))}</code>")
        return
    await db.set_quota(telegram_id, new_quota)
    await state.clear()
    await message.answer(f"Квота пользователя <code>{telegram_id}</code> теперь {new_quota} GB.")


@router.callback_query(F.data.startswith("refreshusage:"))
async def refresh_usage(callback: CallbackQuery, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    telegram_id = int(callback.data.split(":", 1)[1])
    await render_user_details(callback, db, nc, config, telegram_id)
    await callback.answer("Данные обновлены")


@router.callback_query(F.data.startswith("resetpass:"))
async def reset_password(callback: CallbackQuery, bot: Bot, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    telegram_id = int(callback.data.split(":", 1)[1])
    user = await db.get_user(telegram_id)
    if not user or not user.get("nc_user_id"):
        await callback.answer("Nextcloud-пользователь еще не создан", show_alert=True)
        return

    password = generate_password()
    try:
        await nc.set_user_value(user["nc_user_id"], "password", password)
    except NextcloudError as exc:
        await callback.answer("Ошибка Nextcloud", show_alert=True)
        await callback.message.answer(f"Не удалось сбросить пароль: <code>{html.escape(str(exc))}</code>")
        return

    await db.set_nextcloud_password(telegram_id, password)
    try:
        await bot.send_message(
            telegram_id,
            "Администратор сбросил пароль для вашего Nextcloud-аккаунта.\n\n"
            f"Логин: <code>{html.escape(user['nc_user_id'])}</code>\n"
            f"Новый пароль: <code>{html.escape(password)}</code>",
        )
        delivery = "Новый пароль отправлен пользователю."
    except Exception:
        logging.exception("Failed to send reset password to %s", telegram_id)
        delivery = "Пароль изменен, но Telegram не доставил сообщение пользователю."

    await callback.message.answer(f"{delivery}\nПользователь: <code>{telegram_id}</code>")
    await callback.answer("Пароль сброшен")


@router.callback_query(F.data.startswith("supporter:"))
async def set_supporter(callback: CallbackQuery, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    _, telegram_id_raw, value_raw = callback.data.split(":")
    telegram_id = int(telegram_id_raw)
    is_supporter = value_raw == "1"
    await db.set_supporter(telegram_id, is_supporter, premium_until(config) if is_supporter else None)
    logging.info("Premium flag changed: telegram_id=%s is_supporter=%s", telegram_id, is_supporter)
    await callback.answer("Премиум-статус обновлен")
    await render_user_details(callback, db, nc, config, telegram_id)


@router.callback_query(F.data.startswith("disable:"))
async def disable_user(callback: CallbackQuery, db: Database, nc: NextcloudClient, config: Config) -> None:
    await set_enabled(callback, db, nc, config, enabled=False)


@router.callback_query(F.data.startswith("enable:"))
async def enable_user(callback: CallbackQuery, db: Database, nc: NextcloudClient, config: Config) -> None:
    await set_enabled(callback, db, nc, config, enabled=True)


async def set_enabled(callback: CallbackQuery, db: Database, nc: NextcloudClient, config: Config, enabled: bool) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    telegram_id = int(callback.data.split(":", 1)[1])
    user = await db.get_user(telegram_id)
    if not user or not user.get("nc_user_id"):
        await callback.answer("Nextcloud-пользователь еще не создан", show_alert=True)
        return
    try:
        if enabled:
            await nc.enable_user(user["nc_user_id"])
        else:
            await nc.disable_user(user["nc_user_id"])
    except NextcloudError as exc:
        await callback.answer("Ошибка Nextcloud", show_alert=True)
        await callback.message.answer(f"Не удалось изменить доступ: <code>{html.escape(str(exc))}</code>")
        return
    await db.set_disabled(telegram_id, not enabled)
    await callback.answer("Готово")
    await render_user_details(callback, db, nc, config, telegram_id)


@router.callback_query(F.data.startswith("deleteask:"))
async def delete_user_ask(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    telegram_id = int(callback.data.split(":", 1)[1])
    user = await db.get_user(telegram_id)
    if not user:
        await callback.answer("Пользователь не найден", show_alert=True)
        return
    await safe_edit_text(
        callback.message,
        "<b>Удаление пользователя</b>\n\n"
        f"Будет удален аккаунт Nextcloud и запись в базе бота.\n"
        f"Пользователь: <code>{telegram_id}</code>",
        reply_markup=delete_confirm_keyboard(telegram_id),
    )
    await callback.answer()


@router.callback_query(F.data.startswith("deleteyes:"))
async def delete_user_confirm(callback: CallbackQuery, bot: Bot, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    telegram_id = int(callback.data.split(":", 1)[1])
    user = await db.get_user(telegram_id)
    if not user:
        await callback.answer("Пользователь уже удален", show_alert=True)
        return

    if user.get("nc_user_id"):
        try:
            await nc.delete_user(user["nc_user_id"])
        except NextcloudError as exc:
            await callback.answer("Ошибка Nextcloud", show_alert=True)
            await callback.message.answer(f"Не удалось удалить Nextcloud-аккаунт: <code>{html.escape(str(exc))}</code>")
            return

    await db.delete_user(telegram_id)
    logging.warning("User deleted: telegram_id=%s nc_user_id=%s", telegram_id, user.get("nc_user_id"))
    try:
        await bot.send_message(telegram_id, "Ваш beta-доступ Nextcloud был удален администратором.")
    except Exception:
        logging.exception("Failed to notify deleted user %s", telegram_id)
    await safe_edit_text(
        callback.message,
        f"Пользователь <code>{telegram_id}</code> удален.",
        reply_markup=admin_keyboard(),
    )
    await callback.answer("Удалено")


@router.callback_query(F.data == "backup")
async def backup_panel(callback: CallbackQuery, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    await safe_edit_text(
        callback.message,
        f"{event_mark('backup')} <b>Бекапы</b>\n\n"
        "Все бекапы сжимаются в .gz, хранятся на сервере и автоматически чистятся по retention.",
        reply_markup=backup_keyboard(),
    )
    await callback.answer()


@router.callback_query(F.data == "backup:db")
async def backup_db(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    path = create_sqlite_backup(db.path, config.backup_dir)
    prune_old_backups(config.backup_dir, config.backup_retention_days)
    logging.info("Manual SQLite backup created: %s", path)
    await callback.message.answer_document(FSInputFile(path), caption="Сжатый SQLite-бекап базы бота")
    await callback.answer("Бекап отправлен")


@router.callback_query(F.data == "backup:json")
async def backup_json(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    path = await create_json_backup(db, config.backup_dir)
    prune_old_backups(config.backup_dir, config.backup_retention_days)
    logging.info("Manual JSON backup created: %s", path)
    await callback.message.answer_document(FSInputFile(path), caption="Сжатый JSON-бекап пользователей")
    await callback.answer("Бекап отправлен")


@router.callback_query(F.data == "backup:list")
async def backup_list(callback: CallbackQuery, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    backups = list_backup_files(config.backup_dir, limit=10)
    if not backups:
        await safe_edit_text(callback.message, "Сжатых SQLite-бекапов пока нет.", reply_markup=backup_keyboard())
        await callback.answer()
        return
    text = f"{event_mark('backup')} <b>Последние SQLite-бекапы</b>\n\n"
    for index, path in enumerate(backups, start=1):
        text += f"{index}. <code>{html.escape(path.name)}</code> ({format_bytes(path.stat().st_size)})\n"
    await safe_edit_text(callback.message, text, reply_markup=backup_keyboard())
    await callback.answer()


@router.callback_query(F.data == "backup:restore")
async def backup_restore_panel(callback: CallbackQuery, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    backups = list_backup_files(config.backup_dir, limit=10)
    if not backups:
        await safe_edit_text(callback.message, "Нет SQLite-бекапов для восстановления.", reply_markup=backup_keyboard())
        await callback.answer()
        return
    items = [(str(index), path.name) for index, path in enumerate(backups)]
    await safe_edit_text(
        callback.message,
        f"{event_mark('backup')} <b>Восстановление бекапа</b>\n\n"
        "Выберите SQLite-бекап. Перед восстановлением будет создан свежий safety-бекап.",
        reply_markup=restore_backup_keyboard(items),
    )
    await callback.answer()


@router.callback_query(F.data.startswith("restore:"))
async def backup_restore(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    backups = list_backup_files(config.backup_dir, limit=10)
    index = int(callback.data.split(":", 1)[1])
    if index < 0 or index >= len(backups):
        await callback.answer("Бекап не найден", show_alert=True)
        return
    safety = create_sqlite_backup(db.path, config.backup_dir)
    restore_sqlite_backup(backups[index], db.path)
    logging.warning("Database restored from %s; safety backup: %s", backups[index], safety)
    await safe_edit_text(
        callback.message,
        f"{event_mark('backup')} База восстановлена из <code>{html.escape(backups[index].name)}</code>.\n\n"
        f"Safety-бекап перед восстановлением: <code>{html.escape(safety.name)}</code>\n"
        "Рекомендуется перезапустить бота.",
        reply_markup=admin_keyboard(),
    )
    await callback.answer("Восстановлено")


@router.callback_query(F.data == "broadcast")
async def broadcast_start(callback: CallbackQuery, state: FSMContext, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    await state.set_state(BroadcastState.waiting_content)
    await callback.message.answer("Отправьте сообщение для рассылки. Можно текст, фото, документ или другой тип сообщения.")
    await callback.answer()


@router.message(BroadcastState.waiting_content)
async def broadcast_collect(message: Message, state: FSMContext, db: Database, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config):
        return
    recipients = await db.approved_telegram_ids()
    await state.update_data(source_chat_id=message.chat.id, source_message_id=message.message_id)
    await state.set_state(BroadcastState.confirming)
    await message.answer(
        f"Рассылка будет отправлена активным одобренным пользователям: <b>{len(recipients)}</b>.",
        reply_markup=broadcast_confirm_keyboard(),
    )


@router.callback_query(BroadcastState.confirming, F.data == "broadcast:cancel")
async def broadcast_cancel(callback: CallbackQuery, state: FSMContext, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    await state.clear()
    await safe_edit_text(callback.message, "Рассылка отменена.", reply_markup=admin_keyboard())
    await callback.answer()


@router.callback_query(BroadcastState.confirming, F.data == "broadcast:confirm")
async def broadcast_send(callback: CallbackQuery, state: FSMContext, bot: Bot, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return

    data = await state.get_data()
    recipients = await db.approved_telegram_ids()
    sent = 0
    failed = 0
    for telegram_id in recipients:
        try:
            await bot.copy_message(
                chat_id=telegram_id,
                from_chat_id=data["source_chat_id"],
                message_id=data["source_message_id"],
            )
            sent += 1
            await asyncio.sleep(0.05)
        except Exception:
            failed += 1
            logging.exception("Failed to broadcast to %s", telegram_id)

    await state.clear()
    await safe_edit_text(
        callback.message,
        f"Рассылка завершена.\n\nОтправлено: <b>{sent}</b>\nОшибок: <b>{failed}</b>",
        reply_markup=admin_keyboard(),
    )
    await callback.answer("Готово")


def configure_logging(config: Config) -> None:
    config.log_dir.mkdir(parents=True, exist_ok=True)
    log_format = "%(asctime)s %(levelname)s [%(name)s] %(message)s"
    handlers: list[logging.Handler] = [
        logging.StreamHandler(),
        RotatingFileHandler(
            config.log_dir / "bot.log",
            maxBytes=5 * 1024 * 1024,
            backupCount=5,
            encoding="utf-8",
        ),
    ]
    logging.basicConfig(level=logging.INFO, format=log_format, handlers=handlers, force=True)


async def auto_backup_loop(db: Database, config: Config) -> None:
    while True:
        try:
            path = create_sqlite_backup(db.path, config.backup_dir)
            removed = prune_old_backups(config.backup_dir, config.backup_retention_days)
            logging.info("Automatic backup created: %s; removed_old=%s", path, removed)
        except Exception:
            logging.exception("Automatic backup failed")
        await asyncio.sleep(config.auto_backup_interval_hours * 60 * 60)


async def nextcloud_sync_loop(db: Database, nc: NextcloudClient, config: Config) -> None:
    while True:
        try:
            await sync_nextcloud_users(db, nc)
        except Exception:
            logging.exception("Automatic Nextcloud sync failed")
        await asyncio.sleep(config.nextcloud_sync_interval_minutes * 60)


async def premium_expiration_loop(db: Database) -> None:
    while True:
        try:
            expired = await db.expire_supporters()
            if expired:
                logging.info("Expired premium supporters: %s", expired)
        except Exception:
            logging.exception("Premium expiration check failed")
        await asyncio.sleep(60 * 60)


async def upload_worker_loop(
    upload_queue: asyncio.PriorityQueue,
    bot: Bot,
    db: Database,
    nc: NextcloudClient,
    config: Config,
) -> None:
    while True:
        priority, sequence, job = await upload_queue.get()
        try:
            logging.info(
                "Upload worker picked job: telegram_id=%s filename=%s priority=%s sequence=%s queue_left=%s",
                job.telegram_id,
                job.filename,
                priority,
                sequence,
                upload_queue.qsize(),
            )
            await process_upload_job(job, bot, db, nc, config)
        except Exception:
            logging.exception("Upload worker failed")
        finally:
            upload_queue.task_done()


async def main() -> None:
    config = load_config()
    configure_logging(config)
    logging.info("Bot starting. public_nextcloud=%s internal_nextcloud=%s", config.nextcloud_url, config.nextcloud_internal_url)

    db = Database(
        config.database_path,
        service_url=config.database_url,
        api_token=config.database_api_token,
        secret_key=config.database_secret_key,
        premium_days=config.premium_days,
    )
    await db.init()

    nc = NextcloudClient(
        NextcloudCredentials(
            base_url=config.nextcloud_internal_url,
            username=config.nextcloud_admin_user,
            password=config.nextcloud_admin_password,
        )
    )
    platega = (
        PlategaClient(config.platega_merchant_id, config.platega_secret, config.platega_base_url)
        if platega_api_enabled(config)
        else None
    )
    if config.platega_enabled and platega:
        logging.info("Platega API payments enabled")
    elif config.platega_enabled and config.platega_url:
        logging.info("Platega static payment URL enabled")
    bot = Bot(
        token=config.bot_token,
        default=DefaultBotProperties(parse_mode=ParseMode.HTML),
    )
    upload_queue: asyncio.PriorityQueue = asyncio.PriorityQueue()
    dp = Dispatcher(storage=MemoryStorage())
    dp.include_router(router)
    background_tasks = [
        asyncio.create_task(auto_backup_loop(db, config)),
        asyncio.create_task(nextcloud_sync_loop(db, nc, config)),
        asyncio.create_task(premium_expiration_loop(db)),
        asyncio.create_task(upload_worker_loop(upload_queue, bot, db, nc, config)),
    ]
    try:
        await dp.start_polling(bot, db=db, config=config, nc=nc, platega=platega, upload_queue=upload_queue)
    finally:
        for task in background_tasks:
            task.cancel()
        await asyncio.gather(*background_tasks, return_exceptions=True)
        await nc.close()
        if platega:
            await platega.close()
        await db.close()
        await bot.session.close()


if __name__ == "__main__":
    asyncio.run(main())
