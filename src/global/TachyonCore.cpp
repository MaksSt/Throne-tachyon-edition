#include "include/global/TachyonCore.hpp"

#include <QCryptographicHash>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QMutex>
#include <QMutexLocker>
#include <QRegularExpression>
#include <QSysInfo>
#include <QTemporaryFile>
#include <QUrl>

#include <atomic>

#include "include/api/RPC.h"
#include "include/global/Configs.hpp"
#include "include/global/HTTPRequestHelper.hpp"

namespace Throne::TachyonCore {
    namespace {
        QMutex tachyonUpdateMutex;
        std::atomic<bool> tachyonUpdateBusy{false};
        constexpr qint64 kTachyonMaxArchive = 64LL * 1024 * 1024;

        QString tachyonRoot() { return QDir(Configs::GetBasePath()).absoluteFilePath("cores/tachyon"); }

        QString tachyonPlatform() {
            auto arch = QSysInfo::buildCpuArchitecture();
            if (arch == "x86_64") arch = "amd64";
            else if (arch == "aarch64") arch = "arm64";
#if defined(Q_OS_WIN)
            return "windows-" + arch;
#elif defined(Q_OS_MACOS)
            return "darwin-" + arch;
#elif defined(Q_OS_LINUX)
            return "linux-" + arch;
#else
            return {};
#endif
        }

        QByteArray tachyonFileHash(const QString &path) {
            QFile file(path);
            if (!file.open(QIODevice::ReadOnly)) return {};
            QCryptographicHash hash(QCryptographicHash::Sha256);
            if (!hash.addData(&file)) return {};
            return hash.result().toHex();
        }

        bool tachyonOfficialAssetUrl(const QUrl &url) {
            return url.scheme() == "https" && url.host() == "github.com" && url.userInfo().isEmpty() &&
                   url.path().startsWith("/Dushnilin/tachyon-core/releases/download/");
        }

        Status tachyonFailure(const QString &error) {
            auto status = Installed();
            status.error = error;
            MW_show_log(QStringLiteral("[Tachyon update] %1").arg(error));
            return status;
        }
    }

    Status Installed() {
        Status status;
        QFile manifest(tachyonRoot() + "/current.json");
        if (!manifest.open(QIODevice::ReadOnly) || manifest.size() > 65536) return status;
        const auto data = QJsonDocument::fromJson(manifest.readAll()).object();
        const auto relative = data.value("path").toString();
        if (relative.isEmpty() || QDir::isAbsolutePath(relative) || relative.contains('\\') ||
            relative.split('/').contains("..")) return status;
        const QFileInfo file(QDir(tachyonRoot()).filePath(relative));
        const auto canonicalRoot = QFileInfo(tachyonRoot()).canonicalFilePath();
        if (canonicalRoot.isEmpty() || !file.isFile() || !file.isExecutable() ||
            !file.canonicalFilePath().startsWith(canonicalRoot + '/')) return status;
        status.path = file.canonicalFilePath();
        status.version = data.value("version").toString();
        status.archiveSha256 = data.value("archive_sha256").toString();
        status.binarySha256 = data.value("binary_sha256").toString();
        return status;
    }

    QString Executable(QString *error) {
        auto status = Installed();
        if (status.path.isEmpty()) status = UpdateLatest(true);
        if (error) *error = status.error;
        return status.path;
    }

    Status UpdateLatest(bool onlyMissing) {
        QMutexLocker locker(&tachyonUpdateMutex);
        const auto installed = Installed();
        if (onlyMissing && !installed.path.isEmpty()) return installed;
        const auto platform = tachyonPlatform();
        if (platform.isEmpty()) return tachyonFailure(QStringLiteral("No Tachyon release is available for this platform."));
        MW_show_log(QStringLiteral("[Tachyon update] Checking the latest stable GitHub release."));
        HttpGetOptions options;
        options.strictTls = true;
        options.maxBytes = 4 * 1024 * 1024;
        options.headers = {{"Accept", "application/vnd.github+json"}, {"X-GitHub-Api-Version", "2022-11-28"}};
        const auto release = NetworkRequestHelper::HttpGet(
            QStringLiteral("https://api.github.com/repos/Dushnilin/tachyon-core/releases/latest"), options);
        if (!release.error.isEmpty()) return tachyonFailure(release.error);
        const auto json = QJsonDocument::fromJson(release.data).object();
        const auto version = json.value("tag_name").toString();
        static const QRegularExpression tagPattern(QStringLiteral("^v?[0-9][0-9A-Za-z._-]{0,79}$"));
        if (!tagPattern.match(version).hasMatch() || json.value("draft").toBool() || json.value("prerelease").toBool())
            return tachyonFailure(QStringLiteral("GitHub did not return a stable Tachyon release."));
        const auto extension = platform.startsWith("windows-") ? ".zip" : ".tar.gz";
        QStringList assetNames{"tachyon-core-" + platform + "-" + version + extension};
        if (platform == "linux-arm64") assetNames << "tachyon-core-aarch64-" + version + extension;
        else if (platform == "linux-arm") assetNames << "tachyon-core-armv7-" + version + extension;
        QString assetName;
        QJsonObject asset;
        for (const auto &candidate : assetNames) {
            for (const auto item : json.value("assets").toArray()) {
                if (item.toObject().value("name").toString() == candidate) { asset = item.toObject(); assetName = candidate; break; }
            }
            if (!asset.isEmpty()) break;
        }
        if (asset.isEmpty()) return tachyonFailure(QStringLiteral("The latest Tachyon release has no %1 build.").arg(platform));
        const auto digest = asset.value("digest").toString();
        static const QRegularExpression shaPattern(QStringLiteral("^sha256:([0-9a-fA-F]{64})$"));
        const auto shaMatch = shaPattern.match(digest);
        const QUrl url(asset.value("browser_download_url").toString());
        const auto expectedSize = asset.value("size").toInteger();
        if (!shaMatch.hasMatch() || expectedSize <= 0 || expectedSize > kTachyonMaxArchive || !tachyonOfficialAssetUrl(url))
            return tachyonFailure(QStringLiteral("The GitHub asset has no valid SHA-256 digest, URL or size."));
        const auto sha256 = shaMatch.captured(1).toLower();
        if (installed.version == version && installed.archiveSha256 == sha256 &&
            tachyonFileHash(installed.path) == installed.binarySha256.toLatin1()) {
            MW_show_log(QStringLiteral("[Tachyon update] %1 is already current.").arg(version));
            return installed;
        }
        MW_show_log(QStringLiteral("[Tachyon update] Downloading %1.").arg(assetName));
        options.maxBytes = kTachyonMaxArchive;
        options.headers.clear();
        const auto archive = NetworkRequestHelper::HttpGet(url.toString(), options);
        if (!archive.error.isEmpty()) return tachyonFailure(archive.error);
        if (archive.data.size() != expectedSize || QCryptographicHash::hash(archive.data, QCryptographicHash::Sha256).toHex() != sha256.toLatin1())
            return tachyonFailure(QStringLiteral("Tachyon archive SHA-256 or size mismatch. The installed core was kept."));
        QTemporaryFile file(QDir::tempPath() + "/throne-tachyon-XXXXXX");
        if (!file.open() || file.write(archive.data) != archive.data.size() || !file.flush())
            return tachyonFailure(QStringLiteral("Could not save the Tachyon download."));
        const auto archivePath = file.fileName();
        file.close();
        bool rpcOK = false;
        const auto result = API::defaultClient->InstallTachyon(&rpcOK, archivePath, tachyonRoot(), version, sha256);
        if (!rpcOK) return tachyonFailure(QStringLiteral("Tachyon installation failed: IPC error."));
        const auto error = QString::fromStdString(result.error.value_or(""));
        if (!error.isEmpty()) return tachyonFailure(error);
        auto status = Installed();
        if (status.path.isEmpty()) return tachyonFailure(QStringLiteral("Tachyon installation did not publish an executable."));
        status.changed = result.changed.value_or(false);
        MW_show_log(QStringLiteral("[Tachyon update] Installed %1. New connections use this release; running connections are unchanged.").arg(status.version));
        return status;
    }

    bool Update(const std::function<void(const Status &)> &done) {
        if (tachyonUpdateBusy.exchange(true)) return false;
        runOnNewThread([done] {
            const auto status = UpdateLatest();
            runOnUiThread([done, status] {
                tachyonUpdateBusy.store(false);
                if (done) done(status);
            });
        });
        return true;
    }
}
