根据豆瓣id获取IMDB id接口
```json
GET https://api.wmdb.tv/movie/api?id=36318037

{
    "data": [
        {
            "poster": "https://img.wmdb.tv/movie/poster/1768955446198-f7a042.webp",
            "name": "唐朝诡事录之长安",
            "genre": "动作/悬疑/惊悚/犯罪/奇幻/冒险",
            "description": "先天二年夏秋之交，天子虽已登基，但大长公主势力更盛，长安局势阴晴难定。卢凌风（杨旭文 饰）和苏无名（杨志刚 饰），奉命带着由裴喜君、费鸡师、樱桃和薛环组成的探案小队，以护送康国所献金桃的名义重返长安。...",
            "language": "汉语普通话",
            "country": "中国大陆",
            "lang": "Cn",
            "shareImage": ""
        },
        {
            "poster": "https://img.wmdb.tv/movie/poster/noposter.jpg",
            "name": "Tang Chao Gui Shi Lu Zhi Chang'an",
            "genre": "Drama",
            "description": "N/A",
            "language": "Chinese",
            "country": "China",
            "lang": "En",
            "shareImage": ""
        }
    ],
    "writer": [
        {
            "data": [
                {
                    "name": "魏风华",
                    "lang": "Cn"
                },
                {
                    "name": "Fenghua Wei",
                    "lang": "En"
                }
            ]
        }
    ],
    "actor": [
        {
            "data": [
                {
                    "name": "陈创",
                    "lang": "Cn"
                },
                {
                    "name": "Chuang Chen",
                    "lang": "En"
                }
            ]
        },
        {
            "data": [
                {
                    "name": "石悦安鑫",
                    "lang": "Cn"
                },
                {
                    "name": "Anson Shi",
                    "lang": "En"
                }
            ]
        },
        {
            "data": [
                {
                    "name": "杨志刚",
                    "lang": "Cn"
                },
                {
                    "name": "Zhigang Yang",
                    "lang": "En"
                }
            ]
        },
        {
            "data": [
                {
                    "name": "孙雪宁",
                    "lang": "Cn"
                },
                {
                    "name": "Xuening Sun",
                    "lang": "En"
                }
            ]
        },
        {
            "data": [
                {
                    "name": "杨旭文",
                    "lang": "Cn"
                },
                {
                    "name": "Xuwen Yang",
                    "lang": "En"
                }
            ]
        },
        {
            "data": [
                {
                    "name": "岳丽娜",
                    "lang": "Cn"
                },
                {
                    "name": "Lina Yue",
                    "lang": "En"
                }
            ]
        },
        {
            "data": [
                {
                    "name": "郜思雯",
                    "lang": "Cn"
                },
                {
                    "name": "Siwen Gao",
                    "lang": "En"
                }
            ]
        }
    ],
    "director": [
        {
            "data": [
                {
                    "name": "巨兴茂",
                    "lang": "Cn"
                },
                {
                    "name": "Xingmao Ju",
                    "lang": "En"
                }
            ]
        }
    ],
    "originalName": "唐朝诡事录之长安",
    "imdbVotes": 0,
    "imdbRating": "",
    "rottenRating": "",
    "rottenVotes": 0,
    "year": "2025–",
    "imdbId": "tt34387186",
    "alias": "唐朝诡事录3 / 唐朝诡事录 第三部 / 唐朝诡事录·长安 / 唐朝诡事录 第三季 / 唐诡3 / Horror Stories of Tang Dynasty Ⅲ / Strange Legend of Tang Dynasty Ⅲ / Strange Tales of Tang Dynasty Ⅲ",
    "doubanId": "36318037",
    "type": "TVSeries",
    "doubanRating": "8.0",
    "doubanVotes": 162977,
    "duration": 3000,
    "episodes": 40,
    "totalSeasons": 1,
    "dateReleased": "2025-11-08",
    "artRatings": 0,
    "actorRatings": 0,
    "soundRatings": 0,
    "storyRatings": 0,
    "enjoymentRatings": 0,
    "totalVotes": 0
}
```
根据IMDB id获取TMDB电影信息

```json
GET https://api.themoviedb.org/3/find/${imdbId}?external_source=imdb_id&language=zh-CN

headers: {
            "Authorization": `Bearer ${token}`,
            "Content-Type": "application/json"
          }
返回
{
  "movie_results": [
    {
      "adult": false,
      "backdrop_path": "/zfbjgQE1uSd9wiPTX4VzsLi0rGG.jpg",
      "id": 278,
      "title": "肖申克的救赎",
      "original_title": "The Shawshank Redemption",
      "overview": "1947年，小有成就的青年银行家安迪因涉嫌杀害妻子及她的情人而锒铛入狱。在这座名为肖申克的监狱内，希望似乎虚无缥缈，终身监禁的惩罚无疑注定了安迪接下来灰暗绝望的人生。未过多久，安迪尝试接近囚犯中颇有声望的瑞德，请求对方帮自己搞来小锤子。以此为契机，二人逐渐熟络，安迪也仿佛在鱼龙混杂、罪恶横生、黑白混淆的牢狱中找到属于自己的求生之道。他利用自身的专业知识，帮助监狱管理层逃税、洗黑钱，同时凭借与瑞德的交往在犯人中间也渐渐受到礼遇。表面看来，他已如瑞德那样对那堵高墙从憎恨转变为处之泰然，但是对自由的渴望仍促使他朝着心中的希望和目标前进。而关于其罪行的真相，似乎更使这一切朝前推进了一步。",
      "poster_path": "/aAdnwqwkKX5PPcr8EdtaiA8AZVl.jpg",
      "media_type": "movie",
      "original_language": "en",
      "genre_ids": [
        18,
        80
      ],
      "popularity": 29.7348,
      "release_date": "1994-09-23",
      "video": false,
      "vote_average": 8.7,
      "vote_count": 29643
    }
  ],
  "person_results": [],
  "tv_results": [],
  "tv_episode_results": [],
  "tv_season_results": []
}
```

用 TMDb ID 获取剧照信息

```json
电影 GET https://api.themoviedb.org/3/movie/229891/images?include_image_language=zh,en,null
电视剧 GET https://api.themoviedb.org/3/tv/229891/images?include_image_language=zh,en,null
{
    "backdrops": [
        {
            "aspect_ratio": 1.778,
            "height": 1152,
            "iso_3166_1": null,
            "iso_639_1": null,
            "file_path": "/svnZYhAboLrwHEtkKRm2mIdjocB.jpg",
            "vote_average": 8.362,
            "vote_count": 7,
            "width": 2048
        },
        {
            "aspect_ratio": 1.777,
            "height": 1909,
            "iso_3166_1": "US",
            "iso_639_1": "en",
            "file_path": "/kn4RmKUMA1C7oBU9JOesEz6ZXG9.jpg",
            "vote_average": 3.334,
            "vote_count": 1,
            "width": 3393
        }
    ],
    "id": 229891,
    "logos": [
        {
            "aspect_ratio": 15.5,
            "height": 46,
            "iso_3166_1": "US",
            "iso_639_1": "en",
            "file_path": "/dbg6KO0Glp9RYHIt46I0CGXhaB7.png",
            "vote_average": 3.334,
            "vote_count": 1,
            "width": 713
        },
        {
            "aspect_ratio": 5.672,
            "height": 201,
            "iso_3166_1": "TW",
            "iso_639_1": "zh",
            "file_path": "/7g3ist8rWh7zKmIUcEZ9avw3jjY.png",
            "vote_average": 3.334,
            "vote_count": 1,
            "width": 1140
        }
    ],
    "posters": [
        {
            "aspect_ratio": 0.667,
            "height": 2400,
            "iso_3166_1": "CN",
            "iso_639_1": "zh",
            "file_path": "/kDd4zcu7ZxshPabiznEHQfl7khz.jpg",
            "vote_average": 10.0,
            "vote_count": 4,
            "width": 1600
        },
        {
            "aspect_ratio": 0.675,
            "height": 2222,
            "iso_3166_1": "US",
            "iso_639_1": "en",
            "file_path": "/zgUh4cgalSzBjbsT5P0qmU7Rjzk.jpg",
            "vote_average": 3.334,
            "vote_count": 2,
            "width": 1500
        }
    ]
}
backdrops：横版，通常用于背景或电影剧照。
posters：竖版，正式海报。
stills：仅出现在电视剧单集接口中，代表该集的特写镜头。

海报 URL 拼接：
https://image.tmdb.org/t/p/w500{poster_path}
剧照 URL 拼接：
https://image.tmdb.org/t/p/w500{file_path}
```