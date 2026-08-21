// Cross-seeding is the only free ratio there is: the same bytes, already on this disk, seeded on
// every private tracker that also has the release. Nothing is downloaded twice; matches are
// hardlinked, so a cross-seed costs an inode and no space.
//
// Secrets come from the environment, because this file is in a public repo.
const prowlarr = "http://prowlarr:9696";
const key = process.env.PROWLARR_API_KEY;

// Private trackers only. A cross-seed on a public tracker earns nothing: there is no account and no
// ratio there, so it would only put this address in one more public swarm.
const privateIndexers = {
    17: "TorrentLeech",
    16: "DigitalCore",
    11: "BTSCHOOL",
    9: "C411",
    12: "retrotoon (generic torznab)",
};

module.exports = {
    torznab: Object.keys(privateIndexers).map((id) => `${prowlarr}/${id}/api?apikey=${key}`),

    // What qBittorrent already holds is the search list: every .torrent it has is a candidate.
    torrentDir: "/qbit-config/qBittorrent/BT_backup",

    action: "inject",
    // inject, so nothing is written here; cross-seed asks for it to be null outright.
    outputDir: null,
    torrentClients: [
        `qbittorrent:http://${process.env.QBIT_USER}:${encodeURIComponent(process.env.QBIT_PASSWORD)}@qbittorrent:8080`,
    ],

    // Hardlinks land on the same mergerfs branch as the file they point at, so the link dir sits
    // inside downloads: that is the only path qBittorrent has mounted, and a torrent it cannot see
    // is a torrent it cannot seed.
    //
    // `dataDirs` is deliberately absent. Data-based matching hunts for files that have no torrent,
    // and here everything on disk already has one in qBittorrent, so it would only add risk. It is
    // also what forbids a link dir inside it, which this needs.
    linkDirs: ["/data/downloads/cross-seed"],
    linkType: "hardlink",
    matchMode: "flexible",

    // Inject as "<category>.cross-seed" rather than into the *arr's own category. Radarr would see
    // a torrent it has no record of, fail to import it, and raise the orphan-queue alert; with its
    // own category it is invisible to the *arrs and still visible to qbit_manage.
    duplicateCategories: true,

    // Verify before announcing. A flexible match can be a near-miss, and seeding bad data to a
    // private tracker is worse than not seeding at all.
    skipRecheck: false,

    // Building a season pack out of loose episodes needs data-based matching and a particular
    // folder depth, neither of which is set up here. Torrent-for-torrent matches first.
    seasonFromEpisodes: null,
    includeSingleEpisodes: false,
    includeNonVideos: false,

    // Rate: these are private trackers with request limits, and there is no hurry.
    delay: 30,
    searchCadence: "1 day",
    rssCadence: "30 minutes",
    // Daemon mode enforces a ratio here: excludeOlder has to be 2 to 5 times excludeRecentSearch.
    // So: only chase releases from the last 180 days, and do not re-search the same torrent more
    // often than every 45 days. Anything newer than that arrives through rssCadence instead.
    excludeOlder: "180d",
    excludeRecentSearch: "45d",

    apiAuth: false,
    port: 2468,
    host: "0.0.0.0",
    verbose: false,
};
