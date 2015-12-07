/*
 * Copyright (c) 2015, Psiphon Inc.
 * All rights reserved.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package psiphon

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

// DownloadUpgrade performs a tunneled, resumable download of client upgrade files.
// While downloading/resuming, a temporary file is used. Once the download is complete,
// a notice is issued and the upgrade is available at the destination specified in
// config.UpgradeDownloadFilename.
// NOTE: this code does not check that any existing file at config.UpgradeDownloadFilename
// is actually the version specified in clientUpgradeVersion.
//
// BUG: a download that resumes after automation replaces the server-side upgrade entity
// will end up with corrupt data (some part of the older entity, followed by part of
// the newer entity). This is not fatal since authentication of the upgrade package will
// will detect this and the upgrade will be re-downloaded in its entirety. A fix would
// involve storing the entity ETag with the partial download and using If-Range
// (http://www.w3.org/Protocols/rfc2616/rfc2616-sec14.html#sec14.27), or, since S3 doesn't
// list the If-Range header as supported
// (http://docs.aws.amazon.com/AmazonS3/latest/API/RESTObjectGET.html), If-Match followed
// be a re-request on failure.
func DownloadUpgrade(config *Config, clientUpgradeVersion string, tunnel *Tunnel) error {

	// Check if complete file already downloaded
	if _, err := os.Stat(config.UpgradeDownloadFilename); err == nil {
		NoticeClientUpgradeDownloaded(config.UpgradeDownloadFilename)
		return nil
	}

	httpClient, err := MakeTunneledHttpClient(config, tunnel, DOWNLOAD_UPGRADE_TIMEOUT)
	if err != nil {
		return ContextError(err)
	}

	partialFilename := fmt.Sprintf(
		"%s.%s.part", config.UpgradeDownloadFilename, clientUpgradeVersion)

	file, err := os.OpenFile(partialFilename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return ContextError(err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return ContextError(err)
	}

	request, err := http.NewRequest("GET", config.UpgradeDownloadUrl, nil)
	if err != nil {
		return ContextError(err)
	}
	request.Header.Add("Range", fmt.Sprintf("bytes=%d-", fileInfo.Size()))

	response, err := httpClient.Do(request)

	// The resumeable download may ask for bytes past the resource range
	// since it doesn't store the "completed download" state. In this case,
	// the HTTP server returns 416. Otherwise, we expect 206.
	if err == nil &&
		(response.StatusCode != http.StatusPartialContent &&
			response.StatusCode != http.StatusRequestedRangeNotSatisfiable) {
		response.Body.Close()
		err = fmt.Errorf("unexpected response status code: %d", response.StatusCode)
	}
	if err != nil {
		return ContextError(err)
	}
	defer response.Body.Close()

	n, err := io.Copy(NewSyncFileWriter(file), response.Body)
	if err != nil {
		return ContextError(err)
	}

	NoticeInfo("client upgrade downloaded bytes: %d", n)

	// Ensure the file is flushed to disk. The deferred close
	// will be a noop when this succeeds.
	err = file.Close()
	if err != nil {
		return ContextError(err)
	}

	err = os.Rename(partialFilename, config.UpgradeDownloadFilename)
	if err != nil {
		return ContextError(err)
	}

	NoticeClientUpgradeDownloaded(config.UpgradeDownloadFilename)

	return nil
}
