export GOPATH=$(abspath .)

deploy:
	appcfg.py -A boss-hog -V 1 update appengine/
# doesn't seem to work with GOPATH
#	gcloud preview app deploy --project=boss-hog --version=1 appengine/app.yaml

serve:
	dev_appserver.py --port 4444 --admin_port 4445 appengine
