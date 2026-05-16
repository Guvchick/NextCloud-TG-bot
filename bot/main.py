from __future__ import annotations

import asyncio
import html
import logging
import secrets
import string

from aiogram import Bot, Dispatcher, F, Router
from aiogram.client.default import DefaultBotProperties
from aiogram.enums import ParseMode
from aiogram.filters import Command, CommandStart
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from aiogram.fsm.storage.memory import MemoryStorage
from aiogram.types import CallbackQuery, FSInputFile, Message

from bot.backups import create_json_backup, create_sqlite_backup
from bot.config import Config, load_config
from bot.db import Database
from bot.keyboards import (
    admin_keyboard,
    backup_keyboard,
    broadcast_confirm_keyboard,
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


async def admin_summary_text(db: Database) -> str:
    total = await db.count_users()
    requested = await db.count_users("requested")
    approved = await db.count_users("approved")
    rejected = await db.count_users("rejected")
    return (
        "<b>Админ-панель</b>\n\n"
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
async def start(message: Message, bot: Bot, db: Database, config: Config) -> None:
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
        await message.answer(
            "Ваш beta-доступ уже активен.\n\n"
            f"Nextcloud: <b>{html.escape(config.nextcloud_url)}</b>\n"
            f"Логин: <code>{telegram_id}</code>\n"
            f"Квота: <b>{user['quota_gb']} GB</b>\n\n"
            "Если нужен новый пароль, напишите администратору."
        )
        return

    if user["status"] == "rejected":
        await message.answer("Ваша заявка на beta-тест сейчас отклонена.")
        return

    await message.answer("Заявка на beta-тест отправлена администратору. Я сообщу, когда доступ будет готов.")
    admin_text = (
        "<b>Новая заявка на beta-тест</b>\n\n"
        f"Пользователь: {display_name(user)}\n"
        f"Telegram ID: <code>{telegram_id}</code>"
    )
    await notify_admins(bot, config, admin_text, request_review_keyboard(telegram_id))


@router.message(Command("admin"))
async def admin_command(message: Message, db: Database, config: Config) -> None:
    if not message.from_user or not is_admin(message.from_user.id, config):
        return
    await message.answer(await admin_summary_text(db), reply_markup=admin_keyboard())


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

    await db.approve_user(telegram_id, nc_user_id, config.default_quota_gb)
    await bot.send_message(
        telegram_id,
        "Ваша заявка на beta-тест одобрена.\n\n"
        f"Nextcloud: <b>{html.escape(config.nextcloud_url)}</b>\n"
        f"Логин: <code>{nc_user_id}</code>\n"
        f"Пароль: <code>{html.escape(password)}</code>\n"
        f"Место на диске: <b>{config.default_quota_gb} GB</b>\n\n"
        "Сохраните пароль: после отправки бот не показывает его повторно.",
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
async def user_details(callback: CallbackQuery, db: Database, config: Config) -> None:
    if not is_admin(callback.from_user.id, config):
        await callback.answer("Нет доступа", show_alert=True)
        return
    _, telegram_id_raw, back_status, back_page_raw = callback.data.split(":")
    telegram_id = int(telegram_id_raw)
    await render_user_details(callback, db, config, telegram_id, back_status, int(back_page_raw))
    await callback.answer()


async def render_user_details(
    callback: CallbackQuery,
    db: Database,
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
    text = (
        "<b>Пользователь</b>\n\n"
        f"Имя: {display_name(user)}\n"
        f"Telegram ID: <code>{telegram_id}</code>\n"
        f"Nextcloud ID: <code>{html.escape(user.get('nc_user_id') or '-')}</code>\n"
        f"Статус: <b>{html.escape(user['status'])}</b>\n"
        f"Квота: <b>{user['quota_gb']} GB</b>\n"
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
    await render_user_details(callback, db, config, telegram_id)


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
    await render_user_details(callback, db, config, telegram_id)


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
            base_url=config.nextcloud_url,
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
