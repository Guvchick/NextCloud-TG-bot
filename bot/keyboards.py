from __future__ import annotations

from aiogram.types import InlineKeyboardMarkup
from aiogram.utils.keyboard import InlineKeyboardBuilder


def _labels(lang: str) -> dict[str, str]:
    if lang == "en":
        return {
            "change_password": "🔐 Change password",
            "cloud_login": "☁️ Open cloud",
            "support": "💬 Support",
            "donate": "💙 Donate",
            "stars": "⭐ Telegram Stars",
            "platega": "💳 Platega",
            "pay": "💳 Pay",
            "check_payment": "🔎 Check payment",
            "language": "🌐 Language",
            "back": "⬅️ Back",
            "ru": "Русский",
            "en": "English",
        }
    return {
        "change_password": "🔐 Сменить пароль",
        "cloud_login": "☁️ Войти в облако",
        "support": "💬 Поддержка",
        "donate": "💙 Донат",
        "stars": "⭐ Telegram Stars",
        "platega": "💳 Platega",
        "pay": "💳 Оплатить",
        "check_payment": "🔎 Проверить оплату",
        "language": "🌐 Язык",
        "back": "⬅️ Назад",
        "ru": "Русский",
        "en": "English",
    }


def account_keyboard(
    lang: str = "ru",
    show_support: bool = True,
    show_donate: bool = True,
    cloud_url: str | None = None,
) -> InlineKeyboardMarkup:
    labels = _labels(lang)
    builder = InlineKeyboardBuilder()
    if cloud_url:
        builder.button(text=labels["cloud_login"], url=cloud_url)
    builder.button(text=labels["change_password"], callback_data="account:change_password")
    if show_support:
        builder.button(text=labels["support"], callback_data="account:support")
    if show_donate:
        builder.button(text=labels["donate"], callback_data="account:donate")
    builder.button(text=labels["language"], callback_data="account:language")
    builder.adjust(1)
    return builder.as_markup()


def account_back_keyboard(lang: str = "ru") -> InlineKeyboardMarkup:
    labels = _labels(lang)
    builder = InlineKeyboardBuilder()
    builder.button(text=labels["back"], callback_data="account:home")
    builder.adjust(1)
    return builder.as_markup()


def support_keyboard(lang: str = "ru") -> InlineKeyboardMarkup:
    labels = _labels(lang)
    builder = InlineKeyboardBuilder()
    builder.button(text=labels["back"], callback_data="account:home")
    builder.adjust(1)
    return builder.as_markup()


def donate_keyboard(
    lang: str = "ru",
    show_stars: bool = False,
    show_platega: bool = False,
    donate_url: str | None = None,
) -> InlineKeyboardMarkup:
    labels = _labels(lang)
    builder = InlineKeyboardBuilder()
    if show_stars:
        builder.button(text=labels["stars"], callback_data="donate:stars")
    if show_platega:
        builder.button(text=labels["platega"], callback_data="donate:platega")
    if donate_url:
        builder.button(text=labels["donate"], url=donate_url)
    builder.button(text=labels["back"], callback_data="account:home")
    builder.adjust(1)
    return builder.as_markup()


def stars_amounts_keyboard(lang: str = "ru", amounts: tuple[int, ...] = ()) -> InlineKeyboardMarkup:
    labels = _labels(lang)
    builder = InlineKeyboardBuilder()
    for amount in amounts:
        builder.button(text=f"⭐ {amount}", callback_data=f"stars:{amount}")
    builder.button(text=labels["back"], callback_data="account:donate")
    builder.adjust(3, 1)
    return builder.as_markup()


def platega_amounts_keyboard(
    lang: str = "ru",
    amounts: tuple[int, ...] = (),
    static_url: str | None = None,
) -> InlineKeyboardMarkup:
    labels = _labels(lang)
    builder = InlineKeyboardBuilder()
    for amount in amounts:
        builder.button(text=f"💳 {amount} RUB", callback_data=f"platega:{amount}")
    if static_url:
        builder.button(text=labels["pay"], url=static_url)
    builder.button(text=labels["back"], callback_data="account:donate")
    builder.adjust(2, 1, 1)
    return builder.as_markup()


def platega_payment_keyboard(lang: str, payment_url: str, transaction_id: str) -> InlineKeyboardMarkup:
    labels = _labels(lang)
    builder = InlineKeyboardBuilder()
    builder.button(text=labels["pay"], url=payment_url)
    builder.button(text=labels["check_payment"], callback_data=f"platega_check:{transaction_id}")
    builder.button(text=labels["back"], callback_data="donate:platega")
    builder.adjust(1)
    return builder.as_markup()


def language_keyboard(lang: str = "ru") -> InlineKeyboardMarkup:
    labels = _labels(lang)
    builder = InlineKeyboardBuilder()
    builder.button(text=f"🇷🇺 {labels['ru']}", callback_data="lang:ru")
    builder.button(text=f"🇬🇧 {labels['en']}", callback_data="lang:en")
    builder.button(text=labels["back"], callback_data="account:home")
    builder.adjust(2, 1)
    return builder.as_markup()


def request_review_keyboard(telegram_id: int) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    builder.button(text="✅ Одобрить", callback_data=f"approve:{telegram_id}")
    builder.button(text="❌ Отклонить", callback_data=f"reject:{telegram_id}")
    builder.adjust(2)
    return builder.as_markup()


def admin_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    builder.button(text="👥 Пользователи", callback_data="users:all:0")
    builder.button(text="📝 Заявки", callback_data="users:requested:0")
    builder.button(text="🗄️ Бекапы", callback_data="backup")
    builder.button(text="📣 Рассылка", callback_data="broadcast")
    builder.button(text="🔄 Синхронизация", callback_data="sync")
    builder.button(text="✨ Стикеры", callback_data="stickers")
    builder.adjust(2, 2, 1)
    return builder.as_markup()


def users_keyboard(users: list[dict], status: str, page: int, has_next: bool) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    for user in users:
        name = user.get("username") or user.get("first_name") or str(user["telegram_id"])
        label = f"{name} | {user['status']} | {user.get('quota_gb', 0)}GB"
        builder.button(text=label[:60], callback_data=f"user:{user['telegram_id']}:{status}:{page}")

    nav_buttons = []
    if page > 0:
        nav_buttons.append(("⬅️ Назад", f"users:{status}:{page - 1}"))
    if has_next:
        nav_buttons.append(("➡️ Вперед", f"users:{status}:{page + 1}"))
    for text, callback_data in nav_buttons:
        builder.button(text=text, callback_data=callback_data)

    builder.button(text="🛠️ В админку", callback_data="admin")
    builder.adjust(1)
    return builder.as_markup()


def user_keyboard(
    telegram_id: int,
    back_status: str,
    back_page: int,
    status: str,
    is_disabled: bool,
    is_supporter: bool,
) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    if status in {"requested", "rejected"}:
        builder.button(text="✅ Одобрить", callback_data=f"approve:{telegram_id}")
    if status == "requested":
        builder.button(text="❌ Отклонить", callback_data=f"reject:{telegram_id}")
    if status == "approved":
        builder.button(text="➕ 1GB", callback_data=f"quotaadd:{telegram_id}:1")
        builder.button(text="➕ 5GB", callback_data=f"quotaadd:{telegram_id}:5")
        builder.button(text="➕ 10GB", callback_data=f"quotaadd:{telegram_id}:10")
        builder.button(text="⚙️ Другое", callback_data=f"quotacustom:{telegram_id}")
        builder.button(text="🔐 Сбросить пароль", callback_data=f"resetpass:{telegram_id}")
        if is_supporter:
            builder.button(text="⭐ Убрать премиум", callback_data=f"supporter:{telegram_id}:0")
        else:
            builder.button(text="⭐ Сделать премиум", callback_data=f"supporter:{telegram_id}:1")
        if is_disabled:
            builder.button(text="🟢 Включить", callback_data=f"enable:{telegram_id}")
        else:
            builder.button(text="🔴 Отключить", callback_data=f"disable:{telegram_id}")
        builder.button(text="🗑️ Удалить", callback_data=f"deleteask:{telegram_id}")
    builder.button(text="⬅️ Назад", callback_data=f"users:{back_status}:{back_page}")
    builder.adjust(2, 3, 1, 1, 1, 1)
    return builder.as_markup()


def delete_confirm_keyboard(telegram_id: int) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    builder.button(text="🗑️ Да, удалить", callback_data=f"deleteyes:{telegram_id}")
    builder.button(text="⬅️ Отмена", callback_data=f"user:{telegram_id}:all:0")
    builder.adjust(1)
    return builder.as_markup()


def backup_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    builder.button(text="🗄️ Создать SQLite", callback_data="backup:db")
    builder.button(text="📦 Создать JSON", callback_data="backup:json")
    builder.button(text="📋 Список", callback_data="backup:list")
    builder.button(text="♻️ Восстановить", callback_data="backup:restore")
    builder.button(text="🛠️ В админку", callback_data="admin")
    builder.adjust(2, 2, 1)
    return builder.as_markup()


def restore_backup_keyboard(backups: list[tuple[str, str]]) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    for backup_id, label in backups:
        builder.button(text=label[:60], callback_data=f"restore:{backup_id}")
    builder.button(text="⬅️ Отмена", callback_data="backup")
    builder.adjust(1)
    return builder.as_markup()


def broadcast_confirm_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    builder.button(text="📣 Отправить всем", callback_data="broadcast:confirm")
    builder.button(text="⬅️ Отмена", callback_data="broadcast:cancel")
    builder.adjust(1)
    return builder.as_markup()
