# RawLoadrr's config, read as Python: the secrets come from the environment, so the file lives in git.
# Only NOBS is configured. Every other tracker module stays inert without its entry here.
import os

config = {
    # Below 1.0.5 upload.py moves this file away to "upgrade" it.
    "version": "1.0.7",

    "DEFAULT": {
        "tmdb_api": os.environ["TMDB_API_KEY"],
        # pixhost needs no account. The rest of the suite's hosts are left out.
        "img_host_1": "pixhost",
        # NOBS asks for at least four; each one is an ffmpeg run over the film.
        "screens": "4",
        "img_size": "500",
        "optimize_images": True,
        "add_logo": False,
        "add_trailer": False,
        "use_global_sigs": False,
        "default_torrent_client": "qbit",
        "sfx_on_prompt": False,
        "inline_imgs": 3,
    },

    "AUTO": {
        "description_folder": None,
        "delay": 0,
        "size_tolerance": 1,
        "dupe_similarity": 80,
    },

    "TRACKERS": {
        "default_trackers": "NOBS",
        "NOBS": {
            "api_key": os.environ["NOBS_APIKEY"],
            # Only on the .torrent built here: after the upload the tracker's own is fetched and seeded.
            "announce_url": f"https://nobs.rawsmoke.net/announce/{os.environ.get('NOBS_PASSKEY', '')}",
            "anon": False,
            # Every share-limit group skips `keep`: an upload is seeded for as long as it stays.
            "qbit_tag": "keep",
        },
    },

    "TORRENT_CLIENTS": {
        "qbit": {
            "torrent_client": "qbit",
            # Off: it looks for an existing torrent of the same file to reuse, and none of the
            # library files is in one under the path they are uploaded from.
            "enable_search": False,
            "qbit_url": "http://qbittorrent",
            "qbit_port": os.environ.get("QBIT_WEBUI_PORT", "8080"),
            "qbit_user": os.environ["QBIT_USER"],
            "qbit_pass": os.environ["QBIT_PASSWORD"],
            # qbit-manage never deletes a `manual` torrent on a timer.
            "qbit_cat": "manual",
            "qbit_tag": "upload",
            "content_layout": "Original",
            "torrent_storage_dir": "/app/tmp/qbit",
        },
    },
}
