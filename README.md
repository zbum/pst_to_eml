# pst_to_eml

Outlook PST 파일을 메시지별 EML로 풀어 하나의 ZIP으로 저장하는 변환 유틸리티입니다.

창은 [go-gui](https://github.com/go-gui-org/go-gui)로 그립니다. 브라우저나 HTML은 쓰지 않습니다. PST 읽기는 [go-pst](https://github.com/mooijtech/go-pst) (Apache-2.0)를 사용합니다.

메일(`IPM.Note`와 라이브러리가 메일로 분류한 항목)만 EML로 내보냅니다. 연락처, 일정, 작업은 건너뜁니다. 첨부 파일은 해당 EML의 MIME 파트로 넣습니다. PST에 원본 인터넷 헤더가 없으면 제목과 표시 이름으로 헤더를 만듭니다. 표시 문자열에 붙은 NUL과 그 밖의 제어 문자는 헤더에서 뺍니다.

본문은 저장된 형식 그대로 보이게 만듭니다. HTML이 있으면 `text/html`이고, 평문과 함께 있으면 `multipart/alternative`입니다. HTML이 `cid:`로 가리키는 그림은 `multipart/related`로 붙이고, 나머지 첨부는 `multipart/mixed`입니다. RTF만 있는 메일은 HTML로 풀리면 HTML로, 아니면 보이는 글만 평문으로 넣습니다.

## 실행

```bash
go run ./cmd/pst2eml
```

PST와 ZIP 경로를 고른 뒤 변환을 누릅니다. ZIP 안 경로는 PST 폴더를 따르고, 파일 이름은 `000001-제목.eml` 형태입니다. 변환이 끝나면 결과 ZIP을 Finder, 탐색기, 또는 파일 관리자에서 열지 묻습니다. 동의하면 macOS와 Windows는 그 ZIP을 선택한 채로 열고, Linux는 ZIP이 있는 폴더를 엽니다.

## 빌드

macOS 바이너리는 Metal 백엔드 때문에 CGO가 필요합니다. Linux와 Windows는 `CGO_ENABLED=0`으로 크로스 컴파일합니다.

```bash
make test
make build-all
```

산출물:

- `dist/pst2eml-linux-amd64`
- `dist/pst2eml-windows-amd64.exe`
- `dist/pst2eml-darwin-arm64`

## Changelog

### 2026-10-07 — feature/reveal-conversion-result

- 변환이 성공하면 결과 ZIP을 Finder, 탐색기, 또는 파일 관리자에서 열지 묻습니다.
- 동의하면 macOS와 Windows는 그 ZIP을 선택한 채로 열고, Linux는 ZIP이 있는 폴더를 엽니다.
- 취소하거나 변환이 실패하면 열지 않습니다. 묻는 동안 변환 버튼은 비활성화됩니다.
- 새 의존성은 없습니다.
