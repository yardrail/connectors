# Mark file_data on POST /file-manager/files as base64 (format: byte), so the
# connector takes it as a File input.
.paths["/file-manager/files"].post.requestBody.content["application/json"].schema.properties.file_data.format = "byte"
