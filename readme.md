# dynamic proxy configuration generator

This program only works for envoy proxy now, but the goal is to make it extensible for any type of proxy.


## overview

Assuming we're using envoy proxy, you can run envoy to listen for incoming traffic and route to specific upstream clusters.  Users can provide configuration (for now, only in the form of a databag), and this application can send it to envoy at runtime, so envoy doesn't need to be restarted.

You can see examples of how this application takes databag input in the form of json files [here](https://github.com/fmgornick/dynamic-proxy/tree/main/databags).

## requirements

1. Go 1.18+
2. envoy 1.22.2+
3. openssl 3.0.5+
4. tmux 3.3+ (optional)
#### OR
1. Docker 20.10.17+
2. openssl 3.0.5+


## quick start

If you want to see a working example of this application, you can run it yourself with the following steps.  But first, you should generate a certificate on the address you'd like to listen on for routing to HTTPS websites.  You can see how to do that [here](#ssl).  

Once an SSL certificate is generated, you can run the program through two methods.  The easiest method is to use the `docker-compose.yml` file I have provided which does most of the heavy lifting, I explain further [here](#docker), otherwise you can try running everything locally by looking [below](#local).

### <a name="local"></a> running locally

If you have tmux installed, I've provided a script that starts the proxy as well as my program in separate tmux sessions.  It also opens up a third session in the `databags` directory, so you can make changes for this program to dynamically update.

You can run the script with the following command (make sure you're in the project's root directory):
```sh
./scripts/run.sh
```
This script sets flags based on environment variables defined in the  [`.env`](https://github.com/fmgornick/dynamic-proxy/blob/main/.env) file, and you can edit the file to alter some of the settings of the dynamic-proxy.  You can view the meaning of all the possible flags [here](#flags).  Note that it'll probably take around 10-15 seconds before envoy updates with the first configuration, so it may not work right away.

If you would rather run everything yourself without the aid of a script, you can do the following...

1. start envoy server (can also run in background with \'&\' suffix):
```sh
envoy -c bootstrap.yml
```
> I suggest using tmux in order to have a window to run envoy in as well as a separate window to run this program with.  Possibly even a third window for dynamically adding / deleting / modifying / moving databag files to update the envoy configuration at runtime

2. run this program (use \'-h\' to see possible flags):
```sh
go run main.go
# or
go build
./dynamic-proxy
```

> here's a list of the possible flags you can set:
> ```
> $ ./dynamic-proxy -h
> Usage of ./dynamic-proxy:
>   -add-http
>     	optional flag for setting up listeners with HTTP compatability
>   -dir string
>     	path to folder containing databag files (default "databags/dev")
>   -ea string
>     	address the proxy's external listener listens on (default "0.0.0.0")
>   -ecn string
>     	common name of external listening address (default "localhost")
>   -ep uint
>     	port number our external listener listens on (default 8888)
>   -ia string
>     	address the proxy's internal listener listens on (default "0.0.0.0")
>   -icn string
>     	common name of internal listening address (default "localhost")
>   -ip uint
>     	port number our internal listener listens on (default 7777)
> ```
> you can get a bit more of a detailed explanation of the flags [here](#flags)


3.  If no flags changed, go to https://localhost:7777 or https://localhost:8888.  Make sure to append the paths specified in the databag backend objects in order to route to a valid upstream.

4. you can add / delete / modify / move files to the directory the application is watching (**default**: `./databags`) and see as the envoy configuration updates real time.


### <a name="docker"></a> run using docker
the much simpler approach is to use docker.  To get everything running properly, you must first generate an SSL cert by running `./add-cert.sh hostname`.

docker-compose sets flags based on environment variables defined in the  [`.env`](https://github.com/fmgornick/dynamic-proxy/blob/main/.env) file, and you can edit the file to alter some of the settings of the dynamic-proxy.  You can view the meaning of all the possible flags [here](#flags).

Once you set the environment variables, you can just run `docker compose up -d`.  This will mount the databag directory onto my `fmgornick/dynamic-proxy` image running on a container titled "app", and can recieve updates when you make changes.

There's also an `envoyproxy/envoy-dev` image running on the container "proxy" which depends on the "app" container.  With all this set up, you can alter the directory being watched by the "app" container and it should automatically update the changes and send them to the "proxy" container.

You can see the output of the containers by running...
```sh
# to see output of envoy proxy
docker logs proxy

# to see output of dynamic proxy app
docker logs app
```

and when you're done with everything, just run `docker compose down` to stop everything and clean up the containers.


## <a name="ssl"></a> generating SSL certificate for HTTPS connection
I currently have a script that generates an SSL certificate + key for any given hostname.  To run it, just type the following command (replacing "hostname" with the hostname you want to generate a certificate for of course):
```sh
./scripts/add-certs.sh <hostname>
```

This will create a new directory called "certs" in the root of this project.  Once the certificate is generated, you'll need to make sure your computer recognizes it.  If you're using a mac, you can do this by going into the 'Keychain Access' app.  Navigate to 'System' on the left sidebar, then go to File -> Import Items...  It will then prompt you to add your hostname.crt file, so just choose it from where you created / moved it (if you put it in the etc folder, then you'll need to do 'CMD + SHIFT + .' to access files in /etc).  Once added, you need to select it and make sure to "Always Trust" the certificate.

Finally, if your certificate isn't for localhost, you must navigate to [app/config/proxy/envoyproxy.go](https://github.com/fmgornick/dynamic-proxy/blob/main/app/config/proxy/envoyproxy.go) and change the filenames of the keys and certs to whatever yours are named.  The place to actually alter the filenames is at the end of the file in the transportSocket function.  I'm planning on changing this in the future so it's no longer hard coded, but for now just deal with it!

## <a name="flags"></a> flag information
- `-add-http`: if you don't want to type the 'https://' prefix every time you try to use the proxy, you can set this flag and this program will add http listeners on the specified port which then just immediately route the their https counterpart.  When this flag is set, the https listeners are automatically set to port 11111 for internal and port 22222 for external.

- `-dir`: this flag specifies the directory this program watches for changes.  So any time a file is change anywhere in the directory (including sub-directories), this program will update the changes and send them to the xds server to notify envoy proxy.

- `-ea`: stands for "external address", this is the address that the proxy will listen on for incoming external traffic outlined in the databags

- `-ecn`: stands for "external common name", this is the fully qualified domain name of the external listener address.  Program uses this value to check for certificates matching the common name for SSL verification

- `-ep`: stands for "external port", this is the port that the proxy will listen on for incoming external traffic outlined in the databags

- `-ia`: stands for "internal address", this is the address that the proxy will listen on for incoming internal traffic outlined in the databags

- `-icn`: stands for "internal common name", this is the fully qualified domain name of the internal listener address.  Program uses this value to check for certificates matching the common name for SSL verification

- `-ip`: stands for "internal port", this is the port that the proxy will listen on for incoming internal traffic outlined in the databags

## warning
If you're having the listener route to both HTTP and HTTPS depending on the path, then chrome might still tell you the address envoy is listening on is not secure, even if you have a certificate.  Chrome treats websites with mixed HTTP and HTTPS content as not secure.  Even if not, Chrome is very weird and will most likely always say your connection is insecure

## usage
The main application for this program is for websites with many upstream routes that are continuously changing and need constant proxy re-configuration.  With this program, you never need to stop the proxy.  The initial idea was to use this program on https://api.target.com and https://api-internal.target.com, to replace the statically configured HAProxy or atleast run alongside it.


## extension
If you would like to add to this project via adding configuration for other proxies, or accepting new user configurations, I tried my best to make this somewhat easily extensible.

For adding a new type of configuration, you just need to add a file in the [parser directory](https://github.com/fmgornick/dynamic-proxy/tree/main/app/parser).  You just need to add implementation for turning the new config into a universal config that all proxies should be able to use defined [here](https://github.com/fmgornick/dynamic-proxy/blob/main/app/config/universal/config.go).  You can see how I made the parser for databags [here](https://github.com/fmgornick/dynamic-proxy/blob/main/app/parser/databag.go).

For adding a new proxy, you would need to add the new proxy config file (maybe some useful helper functions as well) in the [config/proxy directory](https://github.com/fmgornick/dynamic-proxy/tree/main/app/config/proxy).  Then you'll also want to add a file to the [processor directory](https://github.com/fmgornick/dynamic-proxy/tree/main/app/processor) to turn the universal configuration into a specific proxy configuration.

