#include <stdio.h>
#include <curl/curl.h>

int main(void) {
	printf("libcurl %s\n", curl_version());
	return 0;
}
