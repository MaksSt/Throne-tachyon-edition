#pragma once

#include <QString>
#include <functional>

namespace Throne::TachyonCore {
    struct Status {
        QString version;
        QString path;
        QString archiveSha256;
        QString binarySha256;
        QString error;
        bool changed = false;
    };

    // Any thread. Executable installs the latest stable release when missing.
    Status Installed();
    QString Executable(QString *error);
    // Worker thread; serializes downloads and publishes a new immutable release.
    Status UpdateLatest(bool onlyMissing = false);
    // Main thread; done is delivered on the main thread.
    bool Update(const std::function<void(const Status &)> &done = {});
}
