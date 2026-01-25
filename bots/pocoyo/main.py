import os
import platform
from telegram import Update
from telegram.ext import ApplicationBuilder, CommandHandler, ContextTypes

# --- Config ---
TOKEN = os.getenv("TELEGRAM_BOT_TOKEN")

if not TOKEN:
    raise RuntimeError("TELEGRAM_BOT_TOKEN no está definido")

# --- Handlers ---

async def start(update: Update, context: ContextTypes.DEFAULT_TYPE):
    await update.message.reply_text(
        "🤖 Bot de prueba activo\n"
        "Entorno: Raspberry + Docker + Pi-hole + Tailscale"
    )

async def ping(update: Update, context: ContextTypes.DEFAULT_TYPE):
    await update.message.reply_text("🏓 pong")

async def info(update: Update, context: ContextTypes.DEFAULT_TYPE):
    msg = (
        "ℹ️ Info del bot\n"
        f"- Python: {platform.python_version()}\n"
        f"- Sistema: {platform.system()} {platform.release()}"
    )
    await update.message.reply_text(msg)

async def saludarpapa(update: Update, context: ContextTypes.DEFAULT_TYPE):
    msg = (
        "Hola papá, esto es un bot"
    )
    await update.message.reply_text(msg)

# --- App ---

def main():
    app = ApplicationBuilder().token(TOKEN).build()

    app.add_handler(CommandHandler("start", start))
    app.add_handler(CommandHandler("ping", ping))
    app.add_handler(CommandHandler("info", info))
    app.add_handler(CommandHandler("saludarpapa", saludarpapa))
    print("🤖 Bot arrancado (polling)")
    app.run_polling()

if __name__ == "__main__":
    main()
