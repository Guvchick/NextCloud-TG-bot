from __future__ import annotations

import asyncio
import html
import logging
import re
import secrets
import string
import tempfile
from pathlib import Path

from aiogram import Bot, Dispatcher, F, Router
from aiogram.client.default import DefaultBotProperties
from aiogram.enums import ParseMode
from aiogram.filters import Command, CommandStart, StateFilter
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from aiogram.fsm.storage.memory import MemoryStorage
from aiogram.types import CallbackQuery, FSInputFile, Message

from bot.backups import create_json_backup, create_sqlite_backup
from bot.config import Config, load_config
from bot.db import Database
from bot.keyboards import (
    account_keyboard,
    admin_keyboard,
    backup_keyboard,
    broadcast_confirm_keyboard,
    delete_confirm_keyboard,
    request_review_keyboard,
    user_keyboard,
    users_keyboard,
)
from bot.nextcloud import NextcloudClient, NextcloudCredentials, NextcloudError

router = Router()
PAGE_SIZE = 8


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


async def send_sticker(bot: Bot, chat_id: int, sticker_id: str | None) -> None:
    if not sticker_id:
        return
    try:
        await bot.send_sticker(chat_id, sticker_id)
    except Exception:
        logging.exception("Failed to send sticker to %s", chat_id)


def config_sticker(config: Config, event: str) -> str | None:
    return {
        "welcome": config.sticker_welcome,
        "approved": config.sticker_approved,
        "upload_ok": config.sticker_upload_ok,
        "error": config.sticker_error,
    }.get(event)


async def send_event_sticker(bot: Bot, db: Database, config: Config, chat_id: int, event: str) -> None:
    sticker_id = await db.get_setting(f"sticker_{event}") or config_sticker(config, event)
    await send_sticker(bot, chat_id, sticker_id)


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


async def storage_text(user: dict, nc: NextcloudClient) -> str:
    if not user.get("nc_user_id") or not user.get("nc_password"):
        return "Занято: <b>нет данных</b>"
    try:
        quota = await nc.get_quota(user["nc_user_id"], user["nc_password"])
    except NextcloudError as exc:
        logging.warning("Failed to fetch quota for %s: %s", user["telegram_id"], exc)
        return "Занято: <b>не удалось обновить</b>"

    used = quota["used"]
    available = quota["available"]
    total = used + available if used is not None and available is not None and available >= 0 else None
    if total:
        percent = used / total * 100 if used is not None else 0
        return (
            f"Занято: <b>{format_bytes(used)}</b> из <b>{format_bytes(total)}</b>\n"
            f"<code>{usage_bar(used, available)}</code> {percent:.1f}%"
        )
    return f"Занято: <b>{format_bytes(used)}</b>, свободно: <b>{format_bytes(available)}</b>"


async def account_text(user: dict, nc: NextcloudClient, config: Config) -> str:
    password = user.get("nc_password")
    password_line = (
        f"Пароль: <code>{html.escape(password)}</code>\n"
        if password
        else "Пароль: <b>не сохранен</b>\n"
    )
    return (
        "<b>Ваш Nextcloud-диск</b>\n"
        "<code>--------------------------------</code>\n\n"
        f"Сервер: <b>{html.escape(config.nextcloud_url)}</b>\n"
        f"Логин: <code>{html.escape(user.get('nc_user_id') or str(user['telegram_id']))}</code>\n"
        f"{password_line}"
        f"Квота: <b>{user['quota_gb']} GB</b>\n"
        f"Папка загрузок: <code>{html.escape(config.upload_folder or 'корень диска')}</code>\n\n"
        f"{await storage_text(user, nc)}\n\n"
        "Отправьте файл в этот чат, и бот загрузит его в Nextcloud."
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
        await send_event_sticker(bot, db, config, message.chat.id, "welcome")
        await message.answer(await account_text(user, nc, config), reply_markup=account_keyboard())
        return

    if user["status"] == "rejected":
        await message.answer("Ваша заявка на beta-тест сейчас отклонена.")
        return

    await message.answer(
        "<b>Заявка отправлена</b>\n\n"
        "Администратор проверит доступ к beta-тесту. Я сообщу, когда аккаунт будет готов."
    )
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
    try:
        await nc.check_connection()
        status = "Nextcloud API доступен"
    except Exception as exc:
        status = f"Nextcloud API недоступен: <code>{html.escape(str(exc))}</code>"
    await message.answer(
        "<b>Проверка Nextcloud</b>\n\n"
        f"Публичный URL: <code>{html.escape(config.nextcloud_url)}</code>\n"
        f"Внутренний URL: <code>{html.escape(config.nextcloud_internal_url)}</code>\n\n"
        f"{status}"
    )


@router.message(Command("stickers"))
async def stickers_command(message: Message, db: Database, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config):
        return
    await message.answer(await stickers_text(db, config))


async def stickers_text(db: Database, config: Config) -> str:
    settings = await db.list_settings("sticker_")
    return (
        "<b>Стикеры бота</b>\n\n"
        f"welcome: <b>{'есть' if settings.get('sticker_welcome') or config.sticker_welcome else 'нет'}</b>\n"
        f"approved: <b>{'есть' if settings.get('sticker_approved') or config.sticker_approved else 'нет'}</b>\n"
        f"upload_ok: <b>{'есть' if settings.get('sticker_upload_ok') or config.sticker_upload_ok else 'нет'}</b>\n"
        f"error: <b>{'есть' if settings.get('sticker_error') or config.sticker_error else 'нет'}</b>\n\n"
        "Чтобы задать стикер, отправьте команду:\n"
        "<code>/setsticker welcome</code>\n"
        "<code>/setsticker approved</code>\n"
        "<code>/setsticker upload_ok</code>\n"
        "<code>/setsticker error</code>\n\n"
        "После команды отправьте нужный стикер."
    )


@router.callback_query(F.data == "stickers")
async def stickers_panel(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not callback.from_user or not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    await callback.message.edit_text(await stickers_text(db, config), reply_markup=admin_keyboard())
    await callback.answer()


@router.message(Command("setsticker"))
async def set_sticker_command(message: Message, state: FSMContext, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config):
        return
    parts = (message.text or "").split(maxsplit=1)
    if len(parts) != 2 or parts[1].strip() not in {"welcome", "approved", "upload_ok", "error"}:
        await message.answer("Используйте: <code>/setsticker welcome|approved|upload_ok|error</code>")
        return
    event = parts[1].strip()
    await state.set_state(StickerState.waiting_sticker)
    await state.update_data(sticker_event=event)
    await message.answer(f"Теперь отправьте стикер для события <code>{event}</code>.")


@router.message(StickerState.waiting_sticker, F.sticker)
async def save_sticker(message: Message, state: FSMContext, db: Database, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config) or not message.sticker:
        return
    data = await state.get_data()
    event = data["sticker_event"]
    await db.set_setting(f"sticker_{event}", message.sticker.file_id)
    await state.clear()
    await message.answer(f"Стикер для <code>{event}</code> сохранен.")


@router.message(StateFilter(None), F.sticker)
async def sticker_file_id(message: Message, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config) or not message.sticker:
        return
    await message.answer(
        "Это file_id стикера:\n"
        f"<code>{html.escape(message.sticker.file_id)}</code>\n\n"
        "Можно сохранить его командой <code>/setsticker welcome</code> и затем отправить этот стикер еще раз."
    )


@router.callback_query(F.data == "account:status")
async def account_status(callback: CallbackQuery, db: Database, nc: NextcloudClient, config: Config) -> None:
    user = await db.get_user(callback.from_user.id)
    if not user or user["status"] != "approved" or user["is_disabled"]:
        await callback.answer("Доступ не активен", show_alert=True)
        return
    await callback.message.edit_text(await account_text(user, nc, config), reply_markup=account_keyboard())
    await callback.answer("Обновлено")


@router.callback_query(F.data == "account:change_password")
async def account_change_password(callback: CallbackQuery, state: FSMContext, db: Database, config: Config) -> None:
    user = await db.get_user(callback.from_user.id)
    if not user or user["status"] != "approved" or user["is_disabled"]:
        await callback.answer("Доступ не активен", show_alert=True)
        return
    await state.set_state(UserPasswordState.waiting_password)
    await callback.message.answer(
        "Отправьте новый пароль для Nextcloud.\n\n"
        "Минимум 8 символов. После смены бот обновит сохраненный пароль для загрузок."
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
    if not user or user["status"] != "approved" or user["is_disabled"] or not user.get("nc_user_id"):
        await state.clear()
        await message.answer("Доступ не активен.")
        return

    password = (message.text or "").strip()
    if not valid_password(password):
        await message.answer("Пароль должен быть от 8 до 128 символов.")
        return

    try:
        await nc.set_user_value(user["nc_user_id"], "password", password)
    except NextcloudError as exc:
        await message.answer(f"Не удалось сменить пароль: <code>{html.escape(str(exc))}</code>")
        return

    await db.set_nextcloud_password(user["telegram_id"], password)
    await state.clear()
    await send_event_sticker(bot, db, config, message.chat.id, "approved")
    await message.answer(
        "Пароль сменен.\n\n"
        f"Логин: <code>{html.escape(user['nc_user_id'])}</code>\n"
        f"Новый пароль: <code>{html.escape(password)}</code>",
        reply_markup=account_keyboard(),
    )


@router.message(StateFilter(None), F.document | F.photo | F.video | F.audio | F.voice | F.video_note | F.animation)
async def upload_to_nextcloud(message: Message, bot: Bot, db: Database, nc: NextcloudClient, config: Config) -> None:
    if not message.from_user:
        return

    user = await db.get_user(message.from_user.id)
    if not user or user["status"] != "approved" or user["is_disabled"]:
        await message.answer("Загрузка доступна только одобренным активным пользователям.")
        return
    if not user.get("nc_user_id") or not user.get("nc_password"):
        await message.answer(
            "Для этого аккаунта нет сохраненного WebDAV-пароля. Попросите администратора сбросить пароль в панели."
        )
        return

    target = upload_target_from_message(message)
    if not target:
        await message.answer("Не удалось определить файл для загрузки.")
        return
    file_id, filename, file_size = target
    status_message = await message.answer(
        f"Загружаю <b>{html.escape(filename)}</b> ({format_bytes(file_size)}) в Nextcloud..."
    )

    temp_file = tempfile.NamedTemporaryFile(prefix="tg-nextcloud-", delete=False)
    temp_path = Path(temp_file.name)
    temp_file.close()
    try:
        await bot.download(file_id, destination=temp_path)
        remote_path = await nc.upload_file(user["nc_user_id"], user["nc_password"], config.upload_folder, filename, temp_path)
        updated_storage = await storage_text(user, nc)
        await send_event_sticker(bot, db, config, message.chat.id, "upload_ok")
        await status_message.edit_text(
            f"<b>Файл загружен</b>\n\n"
            f"Путь: <code>{html.escape(remote_path)}</code>\n\n"
            f"{updated_storage}"
        )
    except NextcloudError as exc:
        await send_event_sticker(bot, db, config, message.chat.id, "error")
        await status_message.edit_text(f"Не удалось загрузить файл в Nextcloud: <code>{html.escape(str(exc))}</code>")
    except Exception as exc:
        logging.exception("Failed to upload Telegram file to Nextcloud")
        await status_message.edit_text(f"Не удалось обработать файл: <code>{html.escape(str(exc))}</code>")
    finally:
        temp_path.unlink(missing_ok=True)


@router.callback_query(F.data == "admin")
async def admin_panel(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not callback.from_user or not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    await callback.message.edit_text(await admin_summary_text(db), reply_markup=admin_keyboard())
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
    await send_event_sticker(bot, db, config, telegram_id, "approved")
    await bot.send_message(
        telegram_id,
        "<b>Ваша заявка одобрена</b>\n"
        "<code>--------------------------------</code>\n\n"
        f"Nextcloud: <b>{html.escape(config.nextcloud_url)}</b>\n"
        f"Логин: <code>{nc_user_id}</code>\n"
        f"Пароль: <code>{html.escape(password)}</code>\n"
        f"Место на диске: <b>{config.default_quota_gb} GB</b>\n\n"
        "Файлы можно отправлять прямо сюда: бот загрузит их в Nextcloud.\n"
        "Пароль всегда виден в /start, там же его можно сменить.",
        reply_markup=account_keyboard(),
    )
    await callback.message.edit_text(
        f"Доступ выдан пользователю <code>{telegram_id}</code>: {config.default_quota_gb} GB."
    )
    await callback.answer("Одобрено")


@router.callback_query(F.data.startswith("reject:"))
async def reject_user(callback: CallbackQuery, bot: Bot, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    telegram_id = int(callback.data.split(":", 1)[1])
    await db.reject_user(telegram_id)
    try:
        await bot.send_message(telegram_id, "Ваша заявка на beta-тест отклонена.")
    except Exception:
        logging.exception("Failed to notify rejected user %s", telegram_id)
    await callback.message.edit_text(f"Заявка пользователя <code>{telegram_id}</code> отклонена.")
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
    await callback.message.edit_text(text, reply_markup=users_keyboard(users, status, page, has_next))
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
    storage = await storage_text(user, nc) if nc and user["status"] == "approved" else "Занято: <b>нет данных</b>"
    text = (
        "<b>Пользователь</b>\n\n"
        f"Имя: {display_name(user)}\n"
        f"Telegram ID: <code>{telegram_id}</code>\n"
        f"Nextcloud ID: <code>{html.escape(user.get('nc_user_id') or '-')}</code>\n"
        f"Статус: <b>{html.escape(user['status'])}</b>\n"
        f"Квота: <b>{user['quota_gb']} GB</b>\n"
        f"{storage}\n"
        f"Доступ: <b>{'отключен' if disabled else 'активен'}</b>"
    )
    await callback.message.edit_text(
        text,
        reply_markup=user_keyboard(telegram_id, back_status, back_page, user["status"], disabled),
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
    await callback.message.edit_text(
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
    try:
        await bot.send_message(telegram_id, "Ваш beta-доступ Nextcloud был удален администратором.")
    except Exception:
        logging.exception("Failed to notify deleted user %s", telegram_id)
    await callback.message.edit_text(
        f"Пользователь <code>{telegram_id}</code> удален.",
        reply_markup=admin_keyboard(),
    )
    await callback.answer("Удалено")


@router.callback_query(F.data == "backup")
async def backup_panel(callback: CallbackQuery, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    await callback.message.edit_text("<b>Бекапы</b>\n\nВыберите формат.", reply_markup=backup_keyboard())
    await callback.answer()


@router.callback_query(F.data == "backup:db")
async def backup_db(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    path = create_sqlite_backup(db.path, config.backup_dir)
    await callback.message.answer_document(FSInputFile(path), caption="SQLite-бекап базы бота")
    await callback.answer("Бекап отправлен")


@router.callback_query(F.data == "backup:json")
async def backup_json(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    path = await create_json_backup(db, config.backup_dir)
    await callback.message.answer_document(FSInputFile(path), caption="JSON-бекап пользователей")
    await callback.answer("Бекап отправлен")


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
    await callback.message.edit_text("Рассылка отменена.", reply_markup=admin_keyboard())
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
    await callback.message.edit_text(
        f"Рассылка завершена.\n\nОтправлено: <b>{sent}</b>\nОшибок: <b>{failed}</b>",
        reply_markup=admin_keyboard(),
    )
    await callback.answer("Готово")


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    config = load_config()

    db = Database(config.database_path)
    await db.init()

    nc = NextcloudClient(
        NextcloudCredentials(
            base_url=config.nextcloud_internal_url,
            username=config.nextcloud_admin_user,
            password=config.nextcloud_admin_password,
        )
    )
    bot = Bot(
        token=config.bot_token,
        default=DefaultBotProperties(parse_mode=ParseMode.HTML),
    )
    dp = Dispatcher(storage=MemoryStorage())
    dp.include_router(router)

    try:
        await dp.start_polling(bot, db=db, config=config, nc=nc)
    finally:
        await nc.close()
        await bot.session.close()


if __name__ == "__main__":
    asyncio.run(main())
